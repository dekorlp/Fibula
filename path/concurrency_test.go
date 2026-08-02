package path

import (
	"fmt"
	"sync"
	"testing"
)

// TestFoldingIsSafeForConcurrentUse guards a contract that is easy to miss:
// x/text's cases.Caser "may be stateful and should therefore not be shared
// between goroutines". Collision detection runs on every manifest parse, and
// the sync engine will parse manifests in parallel, so a shared Caser here
// would be a data race in production that no sequential test can see.
func TestFoldingIsSafeForConcurrentUse(t *testing.T) {
	paths := make([]string, 200)
	for i := range paths {
		paths[i] = fmt.Sprintf("assets/Pack_%03d/Textur_%03d.png", i, i)
	}

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if err := CheckCollisions(paths); err != nil {
					t.Errorf("CheckCollisions: %v", err)
					return
				}
				_ = FoldKey("assets/Textur.png")
			}
		}()
	}
	wg.Wait()
}
