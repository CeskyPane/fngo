package client

import (
	"context"
	"sync"

	"github.com/ceskypane/fngo/auth"
	"github.com/ceskypane/fngo/auth/epic"
)

type epicTokenProvider struct {
	store auth.TokenStore
	oauth epic.EpicOAuthClient
	mu    sync.Mutex
}

func (p *epicTokenProvider) AccessToken(ctx context.Context) (string, error) {
	tokens, ok, err := p.store.Load(ctx)
	if err != nil {
		return "", err
	}

	if !ok || tokens.AccessToken == "" {
		return "", auth.ErrNoToken
	}

	return tokens.AccessToken, nil
}

func (p *epicTokenProvider) Refresh(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	current, ok, err := p.store.Load(ctx)
	if err != nil {
		return err
	}

	if !ok || current.RefreshToken == "" {
		return auth.ErrNoToken
	}

	next, err := p.refreshUnlocked(ctx, current)
	if err != nil {
		return err
	}

	return p.store.Save(ctx, next)
}

func (p *epicTokenProvider) refreshUnlocked(ctx context.Context, current auth.TokenSet) (auth.TokenSet, error) {
	if p.oauth == nil {
		return auth.TokenSet{}, auth.ErrNoToken
	}

	return p.oauth.TokenByRefresh(ctx, current.RefreshToken)
}

type oauthRefresher struct {
	provider *epicTokenProvider
}

func (r *oauthRefresher) Refresh(ctx context.Context, current auth.TokenSet) (auth.TokenSet, error) {
	r.provider.mu.Lock()
	defer r.provider.mu.Unlock()

	return r.provider.refreshUnlocked(ctx, current)
}
