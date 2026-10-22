package devsync

import (
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"fmt"
	"strings"
	"unicode"
)

// A sync code is 24 words that the app generates; every device of a sync
// account has the same one. It encodes 256 random bits plus an 8-bit
// checksum exactly like a BIP39 mnemonic (11 bits per word from the
// standard English list), so a mistyped word is caught.

// bip39English is the BIP39 English wordlist, unchanged, from
// github.com/bitcoin/bips/blob/master/bip-0039/english.txt (the same list
// ships with the MIT-licensed go-bip39 module).
//
//go:embed bip39_english.txt
var bip39English string

// CodeWords is the length of a sync code.
const CodeWords = 24

var (
	wordlist  = strings.Fields(bip39English)
	wordIndex = func() map[string]int {
		m := make(map[string]int, len(wordlist))
		for i, w := range wordlist {
			m[w] = i
		}
		return m
	}()
)

// NewCode returns a new random sync code.
func NewCode() (string, error) {
	entropy := make([]byte, 32)
	if _, err := rand.Read(entropy); err != nil {
		return "", err
	}
	return encodeCode(entropy), nil
}

// encodeCode turns 32 bytes into 24 words: the entropy followed by the
// first byte of its SHA-256, read 11 bits at a time.
func encodeCode(entropy []byte) string {
	sum := sha256.Sum256(entropy)
	bits := append(append([]byte{}, entropy...), sum[0])
	words := make([]string, CodeWords)
	for i := range words {
		n := 0
		for b := i * 11; b < i*11+11; b++ {
			n = n<<1 | int(bits[b/8]>>(7-b%8)&1)
		}
		words[i] = wordlist[n]
	}
	return strings.Join(words, " ")
}

// CodeError explains why a typed code isn't valid.
type CodeError struct {
	Msg  string
	Word int // 1-based position of the word that isn't in the list (0 = none)
}

func (e *CodeError) Error() string { return e.Msg }

// ParseCode reads a typed or pasted code: case, spaces, line breaks,
// commas and numbering ("1." or "1)") don't matter, and a word can be
// shortened to its first four letters (unique in the list). It returns the
// code's entropy and the code as the app shows it.
func ParseCode(s string) ([]byte, string, error) {
	var words []string
	for _, f := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		if strings.IndexFunc(f, func(r rune) bool { return !unicode.IsDigit(r) }) < 0 {
			continue // numbering
		}
		words = append(words, f)
	}
	if len(words) == 0 {
		return nil, "", &CodeError{Msg: "enter the 24 words of the sync code"}
	}
	idx := make([]int, len(words))
	for i, w := range words {
		n, ok := lookupWord(w)
		if !ok {
			return nil, "", &CodeError{Msg: fmt.Sprintf("word %d, “%s”, isn't a sync code word. Check it for a typo", i+1, w), Word: i + 1}
		}
		idx[i] = n
	}
	if len(words) != CodeWords {
		return nil, "", &CodeError{Msg: fmt.Sprintf("a sync code has %d words; this has %d", CodeWords, len(words))}
	}
	bits := make([]byte, 33)
	for i, n := range idx {
		for b := 0; b < 11; b++ {
			if n>>(10-b)&1 == 1 {
				pos := i*11 + b
				bits[pos/8] |= 1 << (7 - pos%8)
			}
		}
	}
	entropy := bits[:32]
	if sum := sha256.Sum256(entropy); sum[0] != bits[32] {
		return nil, "", &CodeError{Msg: "these words aren't a valid sync code: check them for a word typed wrongly or in the wrong place"}
	}
	return entropy, encodeCode(entropy), nil
}

// lookupWord finds a word, or a four-letter abbreviation (every word of
// the list is unique by its first four letters).
func lookupWord(w string) (int, bool) {
	if n, ok := wordIndex[w]; ok {
		return n, true
	}
	if len(w) != 4 {
		return 0, false
	}
	for i, x := range wordlist {
		if strings.HasPrefix(x, w) {
			return i, true
		}
	}
	return 0, false
}
