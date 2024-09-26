package usercheck

import (
	"encoding/json"
	"net/http"

	"github.com/interline-io/transitland-mw/auth/authn"
)

// AdminDefaultMiddleware uses a default "admin" context.
func AdminDefaultMiddleware(defaultName string) func(http.Handler) http.Handler {
	return NewUserDefaultMiddleware(func() authn.User { return authn.NewCtxUser(defaultName, "", "").WithRoles("admin") })
}

// UserDefaultMiddleware uses a default "user" context.
func UserDefaultMiddleware(defaultName string) func(http.Handler) http.Handler {
	return NewUserDefaultMiddleware(func() authn.User { return authn.NewCtxUser(defaultName, "", "") })
}

// NewUserDefaultMiddleware uses a default "user" context.
func NewUserDefaultMiddleware(cb func() authn.User) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user := cb()
			r = r.WithContext(authn.WithUser(r.Context(), user))
			next.ServeHTTP(w, r)
		})
	}
}

// AdminRequired limits a request to admin privileges.
func AdminRequired(next http.Handler) http.Handler {
	return RoleRequired("admin")(next)
}

// UserRequired limits a request to user privileges.
func UserRequired(next http.Handler) http.Handler {
	return RoleRequired("user")(next)
}

func RoleRequired(role string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			user := authn.ForContext(ctx)
			if user == nil || !user.HasRole(role) {
				http.Error(w, makeJsonError(http.StatusText(http.StatusUnauthorized)), http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func makeJsonError(msg string) string {
	a := map[string]string{
		"error": msg,
	}
	jj, _ := json.Marshal(&a)
	return string(jj)
}