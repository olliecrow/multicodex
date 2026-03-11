package usage

import "context"

type Source interface {
	Name() string
	Fetch(context.Context) (*Summary, error)
	Close() error
}
