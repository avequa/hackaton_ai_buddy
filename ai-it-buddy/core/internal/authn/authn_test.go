package authn

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/avequa/ai-it-buddy/core/internal/domain"
)

const secret = "test-secret-test-secret-test-secret"

func TestIssueVerify(t *testing.T) {
	now := time.Date(2026, 11, 13, 10, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	iss, err := NewIssuer(secret, clock)
	if err != nil {
		t.Fatal(err)
	}
	u := domain.User{ID: "anna.intern", Name: "Анна", Role: "intern", Team: "payments"}
	token, err := iss.Issue(u, AudienceMCP, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := iss.Verify(token, AudienceMCP)
	if err != nil || claims.Subject != u.ID || claims.Role != "intern" || claims.Team != "payments" {
		t.Fatalf("claims = %+v, err = %v", claims, err)
	}
	if _, err := iss.Verify(token, AudienceUI); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("mcp token accepted as ui: %v", err)
	}
	now = now.Add(2 * time.Minute)
	if _, err := iss.Verify(token, AudienceMCP); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token accepted: %v", err)
	}

	other, _ := NewIssuer(secret+"x", clock)
	foreign, _ := other.Issue(u, AudienceMCP, time.Hour)
	if _, err := iss.Verify(foreign, AudienceMCP); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("token of another secret accepted")
	}
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{"sub": "x"}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := iss.Verify(none, AudienceMCP); !errors.Is(err, ErrInvalidToken) {
		t.Fatal("alg=none accepted")
	}
}

func TestNewIssuer_ShortSecret(t *testing.T) {
	if _, err := NewIssuer("short", nil); err == nil {
		t.Fatal("short secret accepted")
	}
	if iss, err := NewIssuer(secret, nil); err != nil || iss.now == nil {
		t.Fatal("default clock")
	}
}
