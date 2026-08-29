package lifecycle

import (
	"math/rand"
	"sync"
	"time"
)

// Backoff provides bounded jittered retries for Manager-owned long-lived
// product services such as the optional Manager Secure MCP Tunnel.
type Backoff struct {
	mu sync.Mutex
	n  int
	r  *rand.Rand
}

func NewBackoff(seed int64) *Backoff {
	return &Backoff{r: rand.New(rand.NewSource(seed))}
}

func (b *Backoff) Next() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	seq := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, 30 * time.Second, 60 * time.Second}
	i := b.n
	if i >= len(seq) {
		i = len(seq) - 1
	} else {
		b.n++
	}
	base := seq[i]
	floor := base / 2
	return floor + time.Duration(b.r.Int63n(int64(base-floor)+1))
}

func (b *Backoff) Reset() {
	b.mu.Lock()
	b.n = 0
	b.mu.Unlock()
}
