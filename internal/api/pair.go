package api

import (
	"crypto/rand"
	"math/big"
	"strings"
	"time"

	"github.com/impiperl/Tweedle/internal/store"
)

// Pairing codes are short enough to type by hand; they are single-use and
// expire after PairCodeTTL.
const (
	codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O, 1/I
	codeLen      = 10
	PairCodeTTL  = 10 * time.Minute
)

// NewPairCode creates and stores a pairing code and returns it.
func NewPairCode(st *store.Store) (string, time.Time, error) {
	var b strings.Builder
	for i := 0; i < codeLen; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(codeAlphabet))))
		if err != nil {
			return "", time.Time{}, err
		}
		b.WriteByte(codeAlphabet[n.Int64()])
	}
	code := b.String()
	exp := time.Now().Add(PairCodeTTL)
	return code, exp, st.AddPairCode(HashSecret(code), exp)
}

// NormalizeCode accepts codes typed with spaces, dashes or in lower case.
func NormalizeCode(c string) string {
	c = strings.ToUpper(c)
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, c)
}

// FormatCode groups a code for display, e.g. "ABCDE-FGHJK".
func FormatCode(c string) string {
	if len(c) == codeLen {
		return c[:5] + "-" + c[5:]
	}
	return c
}
