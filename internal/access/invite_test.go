package access_test

import (
	"testing"
	"time"

	"github.com/jrimmer/chandra/internal/access"
)

func TestGenerateCode_Format(t *testing.T) {
	code := access.GenerateCode()
	if len(code) != len("chandra-inv-")+12 {
		t.Errorf("expected code length %d, got %d (code: %s)", len("chandra-inv-")+12, len(code), code)
	}
	if code[:12] != "chandra-inv-" {
		t.Errorf("expected prefix chandra-inv-, got %s", code[:12])
	}
}

func TestInviteCode_IsExpired(t *testing.T) {
	expired := access.InviteCode{ExpiresAt: time.Now().Add(-time.Hour)}
	if !expired.IsExpired() {
		t.Error("expected expired code to return IsExpired=true")
	}

	active := access.InviteCode{ExpiresAt: time.Now().Add(time.Hour)}
	if active.IsExpired() {
		t.Error("expected active code to return IsExpired=false")
	}
}
