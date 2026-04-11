package schema

import (
	"github.com/Wei-Shaw/sub2api/ent/schema/mixins"

	"entgo.io/ent"
	"entgo.io/ent/dialect"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// UsageScript holds the schema definition for the UsageScript entity.
type UsageScript struct {
	ent.Schema
}

func (UsageScript) Annotations() []schema.Annotation {
	return []schema.Annotation{
		entsql.Annotation{Table: "usage_scripts"},
	}
}

func (UsageScript) Mixin() []ent.Mixin {
	return []ent.Mixin{
		mixins.TimeMixin{},
		mixins.SoftDeleteMixin{},
	}
}

func (UsageScript) Fields() []ent.Field {
	return []ent.Field{
		field.String("base_url_host").
			NotEmpty().
			SchemaType(map[string]string{
				dialect.Postgres: "text",
			}),
		field.String("account_type").
			NotEmpty().
			MaxLen(20),
		field.String("script").
			SchemaType(map[string]string{
				dialect.Postgres: "text",
			}),
		field.Bool("enabled").
			Default(true),
	}
}

func (UsageScript) Indexes() []ent.Index {
	return []ent.Index{
		// Uniqueness enforced by partial index in SQL migration (WHERE deleted_at IS NULL)
		index.Fields("base_url_host", "account_type"),
	}
}
