package usage

import "context"

type Source interface {
	Name() string
	Fetch(context.Context) (*Summary, error)
	Close() error
}

type UsageSource struct {
	primary  Source
	fallback Source
}

func NewUsageSourceForHome(codexHome string) *UsageSource {
	return &UsageSource{
		primary:  NewAppServerSourceForHome(codexHome),
		fallback: NewOAuthSourceForHome(codexHome),
	}
}

func (s *UsageSource) Name() string {
	return "usage"
}

func (s *UsageSource) Fetch(ctx context.Context) (*Summary, error) {
	return fetchWithFallback(ctx, s.primary, s.fallback)
}

func (s *UsageSource) Close() error {
	var firstErr error
	if s.primary != nil {
		if err := s.primary.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if s.fallback != nil {
		if err := s.fallback.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
