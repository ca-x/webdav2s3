package auth

import (
	"context"

	"github.com/example/webdav-s3/ent"
	"github.com/example/webdav-s3/ent/user"
	"golang.org/x/crypto/bcrypt"
)

// DatabaseAuth authenticates against the database.
type DatabaseAuth struct {
	db *ent.Client
}

// NewDatabase creates a database-backed authenticator.
func NewDatabase(db *ent.Client) Authenticator {
	return &DatabaseAuth{db: db}
}

func (a *DatabaseAuth) Authenticate(username, password string) (bool, error) {
	ctx := context.Background()

	u, err := a.db.User.Query().
		Where(
			user.Username(username),
			user.IsEnabled(true),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}

	err = bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password))
	if err != nil {
		return false, nil
	}

	return true, nil
}

// HashPassword creates a bcrypt hash of the password.
func HashPassword(password string) (string, error) {
	bytes, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	return string(bytes), err
}