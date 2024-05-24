package azcheck

import (
	"context"

	"github.com/interline-io/transitland-mw/auth/authn"
	"github.com/interline-io/transitland-mw/auth/authz"
)

type Whoami struct {
	authz.UnimplementedWhoamiServer
}

func (c *Whoami) Me(ctx context.Context, req *authz.MeRequest) (*authz.MeResponse, error) {
	user := authn.ForContext(ctx)
	if user == nil || user.ID() == "" {
		return nil, ErrUnauthorized
	}

	// Return simple MeResponse
	ret := &authz.MeResponse{
		User:          newAzpbUser(user),
		Roles:         user.Roles(),
		IsGlobalAdmin: user.HasRole(GLOBALADMIN_ROLE),
	}
	return ret, nil
}
