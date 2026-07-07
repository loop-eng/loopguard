package discovery

import "time"

type Discoverer interface {
	BasePath() string
	Discover(maxAge time.Duration) []*Session
	Agent() string
}
