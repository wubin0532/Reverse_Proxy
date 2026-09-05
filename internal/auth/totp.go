package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/hex"
	"strings"
	"time"

	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
)

const (
	TOTPPeriod        = 30
	RecoveryCodeCount = 10
)

func GenerateTOTP(account string) (*otp.Key, error) {
	return totp.Generate(totp.GenerateOpts{
		Issuer:      "andey-proxy",
		AccountName: account,
		Period:      TOTPPeriod,
		SecretSize:  20,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
}

// ValidateTOTP accepts the current 30-second counter and one counter on each
// side. The returned counter lets the caller reject in-process replay.
func ValidateTOTP(secret, code string, now time.Time) (uint64, bool) {
	if len(code) != 6 {
		return 0, false
	}
	base := now.Unix() / TOTPPeriod
	for offset := int64(-1); offset <= 1; offset++ {
		counter := base + offset
		if counter < 0 {
			continue
		}
		generated, err := totp.GenerateCodeCustom(secret, time.Unix(counter*TOTPPeriod, 0), totp.ValidateOpts{
			Period:    TOTPPeriod,
			Skew:      0,
			Digits:    otp.DigitsSix,
			Algorithm: otp.AlgorithmSHA1,
		})
		if err == nil && subtle.ConstantTimeCompare([]byte(generated), []byte(code)) == 1 {
			return uint64(counter), true
		}
	}
	return 0, false
}

func GenerateRecoveryCodes() ([]string, []string, error) {
	codes := make([]string, 0, RecoveryCodeCount)
	hashes := make([]string, 0, RecoveryCodeCount)
	for i := 0; i < RecoveryCodeCount; i++ {
		raw := make([]byte, 10) // 80 bits -> 16 Base32 characters.
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		compact := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
		code := compact[0:4] + "-" + compact[4:8] + "-" + compact[8:12] + "-" + compact[12:16]
		codes = append(codes, code)
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return codes, hashes, nil
}

func HashRecoveryCode(code string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", ""))
	digest := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(digest[:])
}

func FindRecoveryCode(hashes []string, code string) int {
	want := HashRecoveryCode(code)
	for i, stored := range hashes {
		if subtle.ConstantTimeCompare([]byte(stored), []byte(want)) == 1 {
			return i
		}
	}
	return -1
}
