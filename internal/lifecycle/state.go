package lifecycle
import("math/rand";"sync";"time")
type DesiredState string
const(DesiredStopped DesiredState="stopped";DesiredRunning DesiredState="running")
type ObservedState string
const(Stopped ObservedState="stopped";Starting ObservedState="starting";Ready ObservedState="ready";Degraded ObservedState="degraded";RetryWait ObservedState="retry_wait";Stopping ObservedState="stopping")
type Phase string
const(PhaseNone Phase="";PhasePreflight Phase="preflight";PhaseOwnedServer Phase="starting_owned_server";PhaseTunnel Phase="starting_tunnel";PhaseProbing Phase="probing";PhaseReady Phase="ready";PhaseStopping Phase="stopping";PhaseRetry Phase="retry_wait")
type Backoff struct{mu sync.Mutex;n int;r *rand.Rand}
func NewBackoff(seed int64)*Backoff{return &Backoff{r:rand.New(rand.NewSource(seed))}}
func(b *Backoff)Next()time.Duration{b.mu.Lock();defer b.mu.Unlock();seq:=[]time.Duration{time.Second,2*time.Second,5*time.Second,10*time.Second,30*time.Second,60*time.Second};i:=b.n;if i>=len(seq){i=len(seq)-1}else{b.n++};base:=seq[i];return time.Duration(b.r.Int63n(int64(base)+1))}
func(b *Backoff)Reset(){b.mu.Lock();b.n=0;b.mu.Unlock()}
