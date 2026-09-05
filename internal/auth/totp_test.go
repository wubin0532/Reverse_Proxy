package auth

import (
	"testing"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

func TestTOTPWindowAndRecoveryCodes(t *testing.T) {
	key, err := GenerateTOTP("admin")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(2_000_000_000, 0)
	for _, offset := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		code, err := totp.GenerateCodeCustom(key.Secret(), now.Add(offset), totp.ValidateOpts{Period: TOTPPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := ValidateTOTP(key.Secret(), code, now); !ok {
			t.Fatalf("code at offset %v was rejected", offset)
		}
	}
	outside, _ := totp.GenerateCodeCustom(key.Secret(), now.Add(60*time.Second), totp.ValidateOpts{Period: TOTPPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if _, ok := ValidateTOTP(key.Secret(), outside, now); ok {
		t.Fatal("code outside the allowed window was accepted")
	}

	codes, hashes, err := GenerateRecoveryCodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(codes) != RecoveryCodeCount || len(hashes) != RecoveryCodeCount {
		t.Fatalf("codes/hashes = %d/%d", len(codes), len(hashes))
	}
	seen := make(map[string]bool)
	for i, code := range codes {
		if len(code) != 19 || seen[code] || FindRecoveryCode(hashes, code) != i {
			t.Fatalf("invalid recovery code %q", code)
		}
		seen[code] = true
		if code == hashes[i] {
			t.Fatal("recovery code stored in plaintext")
		}
	}
}
