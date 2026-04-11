package repository

import (
	"context"

	"github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/ent/usagescript"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type usageScriptRepository struct {
	client *ent.Client
}

func NewUsageScriptRepository(client *ent.Client) service.UsageScriptRepository {
	return &usageScriptRepository{client: client}
}

func (r *usageScriptRepository) FindByHostAndType(ctx context.Context, baseURLHost string, accountType string) (*service.UsageScript, error) {
	m, err := r.client.UsageScript.Query().
		Where(
			usagescript.BaseURLHostEQ(baseURLHost),
			usagescript.AccountTypeEQ(accountType),
			usagescript.EnabledEQ(true),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, nil // no script configured for this host+type
		}
		return nil, err
	}
	return toServiceUsageScript(m), nil
}

func (r *usageScriptRepository) List(ctx context.Context) ([]*service.UsageScript, error) {
	items, err := r.client.UsageScript.Query().
		Order(ent.Desc(usagescript.FieldCreatedAt)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*service.UsageScript, len(items))
	for i, m := range items {
		result[i] = toServiceUsageScript(m)
	}
	return result, nil
}

func (r *usageScriptRepository) Create(ctx context.Context, script *service.UsageScript) (*service.UsageScript, error) {
	m, err := r.client.UsageScript.Create().
		SetBaseURLHost(script.BaseURLHost).
		SetAccountType(script.AccountType).
		SetScript(script.Script).
		SetEnabled(script.Enabled).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toServiceUsageScript(m), nil
}

func (r *usageScriptRepository) Update(ctx context.Context, id int64, script *service.UsageScript) (*service.UsageScript, error) {
	m, err := r.client.UsageScript.UpdateOneID(id).
		SetBaseURLHost(script.BaseURLHost).
		SetAccountType(script.AccountType).
		SetScript(script.Script).
		SetEnabled(script.Enabled).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	return toServiceUsageScript(m), nil
}

func (r *usageScriptRepository) Delete(ctx context.Context, id int64) error {
	return r.client.UsageScript.DeleteOneID(id).Exec(ctx)
}

func toServiceUsageScript(m *ent.UsageScript) *service.UsageScript {
	return &service.UsageScript{
		ID:          m.ID,
		BaseURLHost: m.BaseURLHost,
		AccountType: m.AccountType,
		Script:      m.Script,
		Enabled:     m.Enabled,
		CreatedAt:   m.CreatedAt,
		UpdatedAt:   m.UpdatedAt,
	}
}
