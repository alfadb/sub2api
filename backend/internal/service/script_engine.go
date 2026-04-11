package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	httppool "github.com/Wei-Shaw/sub2api/internal/pkg/httpclient"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

const (
	scriptExecTimeout     = 10 * time.Second
	scriptHTTPTimeout     = 5 * time.Second
	scriptMaxResponseBody = 1 << 20 // 1MB
)

// ScriptEngine Starlark 脚本引擎
type ScriptEngine struct {
	programs sync.Map // scriptHash -> *starlark.Program (compiled cache)
}

func NewScriptEngine() *ScriptEngine {
	return &ScriptEngine{}
}

// Execute 执行用量脚本，返回结构化结果
func (e *ScriptEngine) Execute(ctx context.Context, scriptContent string, account *Account) (*ScriptUsageResult, error) {
	// 1. Get or compile the program
	program, err := e.getOrCompile(scriptContent)
	if err != nil {
		return nil, fmt.Errorf("compile script: %w", err)
	}

	// 2. Create execution context with timeout
	execCtx, cancel := context.WithTimeout(ctx, scriptExecTimeout)
	defer cancel()

	// 3. Build the context dict to inject into the script
	ctxDict := e.buildContextDict(account)

	// 4. Build builtins (http_get, http_post, json_parse)
	builtins := e.buildBuiltins(execCtx)

	// 5. Create thread with cancellation
	thread := &starlark.Thread{
		Name: "usage-script",
	}
	thread.SetLocal("context", execCtx)

	// Set up cancellation: when context is done, cancel the thread
	go func() {
		<-execCtx.Done()
		thread.Cancel("script execution timeout")
	}()

	// 6. Create predeclared with builtins + ctx
	predeclared := starlark.StringDict{
		"ctx":        ctxDict,
		"http_get":   starlark.NewBuiltin("http_get", builtins.httpGet),
		"http_post":  starlark.NewBuiltin("http_post", builtins.httpPost),
		"json_parse": starlark.NewBuiltin("json_parse", builtins.jsonParse),
		"time_now":   starlark.NewBuiltin("time_now", builtinTimeNow),
	}

	// 7. Execute the program
	globals, err := program.Init(thread, predeclared)
	if err != nil {
		return &ScriptUsageResult{Error: fmt.Sprintf("init: %v", err)}, nil
	}

	// 8. Call fetch_usage(ctx)
	fetchUsage, ok := globals["fetch_usage"]
	if !ok {
		return nil, fmt.Errorf("script missing fetch_usage function")
	}

	callable, ok := fetchUsage.(starlark.Callable)
	if !ok {
		return nil, fmt.Errorf("fetch_usage is not callable")
	}

	resultVal, err := starlark.Call(thread, callable, starlark.Tuple{ctxDict}, nil)
	if err != nil {
		return &ScriptUsageResult{Error: fmt.Sprintf("exec: %v", err)}, nil
	}

	// 9. Parse the result
	return e.parseResult(resultVal)
}

func (e *ScriptEngine) getOrCompile(scriptContent string) (*starlark.Program, error) {
	hash := sha256HashStr(scriptContent)

	if cached, ok := e.programs.Load(hash); ok {
		prog, _ := cached.(*starlark.Program)
		return prog, nil
	}

	// Compile (thread-safe: FileOptions is stateless)
	_, program, err := starlark.SourceProgramOptions(&syntax.FileOptions{}, "usage_script.star", scriptContent, func(name string) bool {
		// Only allow predeclared names
		return name == "ctx" || name == "http_get" || name == "http_post" || name == "json_parse" || name == "time_now"
	})
	if err != nil {
		return nil, err
	}

	e.programs.Store(hash, program)
	return program, nil
}

func (e *ScriptEngine) buildContextDict(account *Account) *starlarkstruct.Struct {
	baseURL := account.GetBaseURL()

	// Convert credentials to starlark dict
	credDict := starlark.NewDict(len(account.Credentials))
	for k, v := range account.Credentials {
		_ = credDict.SetKey(starlark.String(k), goToStarlark(v))
	}

	// Convert extra to starlark dict
	extraDict := starlark.NewDict(len(account.Extra))
	for k, v := range account.Extra {
		_ = extraDict.SetKey(starlark.String(k), goToStarlark(v))
	}

	return starlarkstruct.FromStringDict(starlarkstruct.Default, starlark.StringDict{
		"base_url":    starlark.String(baseURL),
		"credentials": credDict,
		"extra":       extraDict,
		"platform":    starlark.String(account.Platform),
		"type":        starlark.String(account.Type),
	})
}

// scriptBuiltins holds builtin functions that need execCtx
type scriptBuiltins struct {
	ctx context.Context
}

func (e *ScriptEngine) buildBuiltins(ctx context.Context) *scriptBuiltins {
	return &scriptBuiltins{ctx: ctx}
}

// httpGet implements http_get(url, headers={}) -> (status_code, body, response_headers)
func (b *scriptBuiltins) httpGet(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var url string
	headers := &starlark.Dict{}
	if err := starlark.UnpackArgs("http_get", args, kwargs, "url", &url, "headers?", &headers); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(b.ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("http_get: create request: %w", err)
	}

	applyStarlarkHeaders(req, headers)

	client, err := httppool.GetClient(httppool.Options{
		Timeout: scriptHTTPTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("http_get: get client: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http_get: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, scriptMaxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("http_get: read body: %w", err)
	}

	return starlark.Tuple{starlark.MakeInt(resp.StatusCode), starlark.String(body), responseHeadersToStarlark(resp)}, nil
}

// httpPost implements http_post(url, headers={}, body="") -> (status_code, body, response_headers)
func (b *scriptBuiltins) httpPost(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var url string
	var bodyStr string
	headers := &starlark.Dict{}
	if err := starlark.UnpackArgs("http_post", args, kwargs, "url", &url, "headers?", &headers, "body?", &bodyStr); err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(b.ctx, http.MethodPost, url, strings.NewReader(bodyStr))
	if err != nil {
		return nil, fmt.Errorf("http_post: create request: %w", err)
	}

	applyStarlarkHeaders(req, headers)

	client, err := httppool.GetClient(httppool.Options{
		Timeout: scriptHTTPTimeout,
	})
	if err != nil {
		return nil, fmt.Errorf("http_post: get client: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http_post: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, scriptMaxResponseBody))
	if err != nil {
		return nil, fmt.Errorf("http_post: read body: %w", err)
	}

	return starlark.Tuple{starlark.MakeInt(resp.StatusCode), starlark.String(body), responseHeadersToStarlark(resp)}, nil
}

// jsonParse implements json_parse(str) -> dict/list
func (b *scriptBuiltins) jsonParse(thread *starlark.Thread, fn *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var s string
	if err := starlark.UnpackPositionalArgs("json_parse", args, kwargs, 1, &s); err != nil {
		return nil, err
	}

	var data any
	if err := json.Unmarshal([]byte(s), &data); err != nil {
		return nil, fmt.Errorf("json_parse: %w", err)
	}

	return goToStarlark(data), nil
}

// builtinTimeNow implements time_now() -> int (current unix timestamp in seconds)
func builtinTimeNow(_ *starlark.Thread, _ *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	if err := starlark.UnpackPositionalArgs("time_now", args, kwargs, 0); err != nil {
		return nil, err
	}
	return starlark.MakeInt64(time.Now().Unix()), nil
}

// parseResult converts the starlark return value to ScriptUsageResult
func (e *ScriptEngine) parseResult(val starlark.Value) (*ScriptUsageResult, error) {
	// Expect a dict with "windows" key
	dict, ok := val.(*starlark.Dict)
	if !ok {
		return nil, fmt.Errorf("fetch_usage must return a dict, got %s", val.Type())
	}

	windowsVal, found, err := dict.Get(starlark.String("windows"))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("fetch_usage result missing 'windows' key")
	}

	windowsList, ok := windowsVal.(*starlark.List)
	if !ok {
		return nil, fmt.Errorf("'windows' must be a list, got %s", windowsVal.Type())
	}

	result := &ScriptUsageResult{
		Windows: make([]ScriptUsageWindow, 0, windowsList.Len()),
	}

	for i := 0; i < windowsList.Len(); i++ {
		wDict, ok := windowsList.Index(i).(*starlark.Dict)
		if !ok {
			continue
		}
		window := ScriptUsageWindow{}

		if v, found, _ := wDict.Get(starlark.String("name")); found {
			if s, ok := v.(starlark.String); ok {
				window.Name = string(s)
			}
		}
		if v, found, _ := wDict.Get(starlark.String("utilization")); found {
			if f, ok := starlark.AsFloat(v); ok {
				window.Utilization = f
			}
		}
		if v, found, _ := wDict.Get(starlark.String("resets_at")); found {
			if intVal, ok := v.(starlark.Int); ok {
				if i64, ok := intVal.Int64(); ok {
					window.ResetsAt = &i64
				}
			}
		}
		if v, found, _ := wDict.Get(starlark.String("used")); found {
			if f, ok := starlark.AsFloat(v); ok {
				window.Used = &f
			}
		}
		if v, found, _ := wDict.Get(starlark.String("limit")); found {
			if f, ok := starlark.AsFloat(v); ok {
				window.Limit = &f
			}
		}
		if v, found, _ := wDict.Get(starlark.String("unit")); found {
			if s, ok := v.(starlark.String); ok {
				window.Unit = string(s)
			}
		}

		result.Windows = append(result.Windows, window)
	}

	// Check for error field
	if v, found, _ := dict.Get(starlark.String("error")); found {
		if s, ok := v.(starlark.String); ok {
			result.Error = string(s)
		}
	}

	return result, nil
}

// Helper functions

func sha256HashStr(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

// responseHeadersToStarlark converts HTTP response headers to a starlark dict.
// Multi-value headers are joined with "; " (e.g. multiple Set-Cookie values).
func responseHeadersToStarlark(resp *http.Response) *starlark.Dict {
	d := starlark.NewDict(len(resp.Header))
	for k, vals := range resp.Header {
		_ = d.SetKey(starlark.String(k), starlark.String(strings.Join(vals, "; ")))
	}
	return d
}

func applyStarlarkHeaders(req *http.Request, headers *starlark.Dict) {
	if headers == nil {
		return
	}
	for _, item := range headers.Items() {
		if k, ok := item[0].(starlark.String); ok {
			if v, ok := item[1].(starlark.String); ok {
				req.Header.Set(string(k), string(v))
			}
		}
	}
}

// goToStarlark converts a Go value to a Starlark value
func goToStarlark(v any) starlark.Value {
	switch v := v.(type) {
	case nil:
		return starlark.None
	case bool:
		return starlark.Bool(v)
	case string:
		return starlark.String(v)
	case float64:
		// Check if it's an integer value
		if v == float64(int64(v)) {
			return starlark.MakeInt64(int64(v))
		}
		return starlark.Float(v)
	case float32:
		return starlark.Float(float64(v))
	case int:
		return starlark.MakeInt(v)
	case int64:
		return starlark.MakeInt64(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return starlark.MakeInt64(i)
		}
		if f, err := v.Float64(); err == nil {
			return starlark.Float(f)
		}
		return starlark.String(v.String())
	case map[string]any:
		d := starlark.NewDict(len(v))
		for k, val := range v {
			_ = d.SetKey(starlark.String(k), goToStarlark(val))
		}
		return d
	case []any:
		elems := make([]starlark.Value, len(v))
		for i, val := range v {
			elems[i] = goToStarlark(val)
		}
		return starlark.NewList(elems)
	default:
		return starlark.String(fmt.Sprintf("%v", v))
	}
}
