package core

import (
	"sort"
	"testing"
)

func TestProviderInstanceIDLessUsesNumericSuffix(t *testing.T) {
	ids := []string{"whatsapp-10", "whatsapp-2", "whatsapp-1", "whatsapp-custom"}
	sort.Slice(ids, func(i, j int) bool { return providerInstanceIDLess(ids[i], ids[j]) })
	want := []string{"whatsapp-1", "whatsapp-2", "whatsapp-10", "whatsapp-custom"}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("sorted IDs = %v, want %v", ids, want)
		}
	}
}
