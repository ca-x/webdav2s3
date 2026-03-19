package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
)

// User holds the schema definition for the User entity.
type User struct {
	ent.Schema
}

// Fields of the User.
func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("username").
			Unique().
			NotEmpty().
			Comment("Login username"),
		field.String("password_hash").
			Sensitive().
			NotEmpty().
			Comment("Bcrypt password hash"),
		field.Enum("role").
			Values("admin", "user").
			Default("user").
			Comment("User role"),
		field.Bool("is_enabled").
			Default(true).
			Comment("Whether user can login"),
		field.Time("created_at").
			Default(time.Now).
			Immutable(),
		field.Time("updated_at").
			Default(time.Now).
			UpdateDefault(time.Now),
	}
}

// Edges of the User.
func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("backends", S3Backend.Type).
			Ref("users"),
	}
}