package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var ErrUnauthenticated = errors.New("unauthenticated")

type Principal struct {
	Actor string
	Role  string
}

type Authenticator struct {
	allowHeaderIdentity bool
	staticTokens        map[string]Principal
}

type contextKey string

const principalContextKey contextKey = "broker_principal"

func NewHeaderAuthenticator() *Authenticator {
	return &Authenticator{allowHeaderIdentity: true}
}

func NewStaticTokenAuthenticator(staticTokens map[string]Principal) *Authenticator {
	return &Authenticator{staticTokens: staticTokens}
}

func WithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, principalContextKey, principal)
}

func PrincipalFromContext(ctx context.Context) Principal {
	principal, _ := ctx.Value(principalContextKey).(Principal)
	return principal
}

func (a *Authenticator) Authenticate(r *http.Request) (Principal, error) {
	if a == nil {
		return PrincipalFromHeaders(r.Header), nil
	}
	if len(a.staticTokens) > 0 {
		token, err := bearerToken(r.Header.Get("Authorization"))
		if err != nil {
			return Principal{}, err
		}
		principal, ok := a.staticTokens[token]
		if !ok {
			return Principal{}, fmt.Errorf("%w: invalid bearer token", ErrUnauthenticated)
		}
		return principal, nil
	}
	if a.allowHeaderIdentity {
		return PrincipalFromHeaders(r.Header), nil
	}
	return Principal{}, fmt.Errorf("%w: no authentication method configured", ErrUnauthenticated)
}

func PrincipalFromHeaders(header http.Header) Principal {
	actor := strings.TrimSpace(header.Get("X-Broker-Actor"))
	if actor == "" {
		actor = "anonymous"
	}
	role := strings.TrimSpace(header.Get("X-Broker-Role"))
	if role == "" {
		role = "user"
	}
	return Principal{
		Actor: actor,
		Role:  role,
	}
}

func ParseStaticTokens(raw string) (map[string]Principal, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	out := make(map[string]Principal)
	entries := strings.Split(raw, ",")
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		tokenAndPrincipal := strings.SplitN(entry, "=", 2)
		if len(tokenAndPrincipal) != 2 {
			return nil, fmt.Errorf("invalid token mapping %q", entry)
		}
		token := strings.TrimSpace(tokenAndPrincipal[0])
		actorAndRole := strings.SplitN(strings.TrimSpace(tokenAndPrincipal[1]), ":", 2)
		if token == "" || len(actorAndRole) != 2 {
			return nil, fmt.Errorf("invalid token mapping %q", entry)
		}
		out[token] = Principal{
			Actor: strings.TrimSpace(actorAndRole[0]),
			Role:  strings.TrimSpace(actorAndRole[1]),
		}
	}
	return out, nil
}

func IsAdmin(principal Principal) bool {
	return strings.EqualFold(principal.Role, "admin")
}

func bearerToken(header string) (string, error) {
	header = strings.TrimSpace(header)
	if header == "" {
		return "", fmt.Errorf("%w: missing bearer token", ErrUnauthenticated)
	}
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || strings.TrimSpace(parts[1]) == "" {
		return "", fmt.Errorf("%w: invalid authorization header", ErrUnauthenticated)
	}
	return strings.TrimSpace(parts[1]), nil
}
