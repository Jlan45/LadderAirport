package hashutil_test

import (
	"testing"

	"github.com/labberairport/pkg/hashutil"
)

func TestSHA256Hex(t *testing.T) {
	// echo -n "hello" | sha256sum
	const want = "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
	got := hashutil.SHA256Hex([]byte("hello"))
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
