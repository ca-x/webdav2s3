package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/edge"
)

// S3Backend holds the schema definition for the S3Backend entity.
type S3Backend struct {
	ent.Schema
}

// Fields of the S3Backend.
func (S3Backend) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").
			Unique().
			NotEmpty().
			Comment("Human-readable backend identifier"),
		field.String("endpoint").
			Optional().
			Comment("S3 API endpoint URL (empty for AWS)"),
		field.String("region").
			Default("us-east-1").
			Comment("AWS region"),
		field.String("bucket").
			NotEmpty().
			Comment("S3 bucket name"),
		field.String("access_key").
			Sensitive().
			NotEmpty().
			Comment("AWS access key ID"),
		field.String("secret_key").
			Sensitive().
			NotEmpty().
			Comment("AWS secret access key"),
		field.String("session_token").
			Optional().
			Sensitive().
			Comment("AWS session token (temporary credentials)"),
		field.Bool("path_style").
			Default(false).
			Comment("Use path-style URLs (required for MinIO/R2)"),
		field.String("key_prefix").
			Optional().
			Comment("Optional prefix for all keys in this backend"),
		field.String("mount_path").
			Unique().
			NotEmpty().
			Comment("Virtual path to mount this backend (e.g., /minio)"),
		field.Bool("is_enabled").
			Default(true).
			Comment("Whether this backend is active"),
		field.Bool("is_readonly").
			Default(false).
			Comment("Whether this backend is read-only"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the S3Backend.
func (S3Backend) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("users", User.Type),
	}
}