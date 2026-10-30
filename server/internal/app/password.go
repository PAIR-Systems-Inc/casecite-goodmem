package app

import (
	_ "embed"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

//go:embed common_passwords.txt
var commonPasswordText string

var passwordUpper = regexp.MustCompile(`[A-Z]`)
var passwordLower = regexp.MustCompile(`[a-z]`)
var passwordDigit = regexp.MustCompile(`[0-9]`)
var passwordNonLetter = regexp.MustCompile(`[^a-z]`)

func passwordErrors(p string) []string {
	errors := []string{}
	if utf8.RuneCountInString(p) < 12 {
		errors = append(errors, "Password must be at least 12 characters")
	}
	if len(p) > 72 {
		errors = append(errors, "Password must not exceed 72 bytes")
	}
	if !passwordUpper.MatchString(p) {
		errors = append(errors, "Password must contain at least one uppercase letter")
	}
	if !passwordLower.MatchString(p) {
		errors = append(errors, "Password must contain at least one lowercase letter")
	}
	if !passwordDigit.MatchString(p) {
		errors = append(errors, "Password must contain at least one digit")
	}
	if !strings.ContainsAny(p, "!@#$%^&*()_+-=[]{};':\"\\|,.<>/?`~") {
		errors = append(errors, "Password must contain at least one special character")
	}
	last, count := rune(0), 0
	for _, r := range p {
		if r == last {
			count++
		} else {
			last, count = r, 1
		}
		if count == 4 {
			errors = append(errors, "Password must not contain more than 3 consecutive identical characters")
			break
		}
	}
	core := strings.TrimFunc(p, func(r rune) bool { return !((r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')) })
	core = strings.NewReplacer("@", "a", "4", "a", "0", "o", "1", "l", "!", "i", "3", "e", "$", "s", "5", "s", "7", "t").Replace(strings.ToLower(core))
	letters := passwordNonLetter.ReplaceAllString(core, "")
	words := strings.Fields(commonPasswordText)
	common := letters == "" || slices.Contains(words, letters)
	for i := 1; i < len(letters) && !common; i++ {
		common = slices.Contains(words, letters[:i]) && slices.Contains(words, letters[i:])
	}
	if common {
		errors = append(errors, "Password is too common; choose something less predictable")
	}
	return errors
}

func validPassword(p string) bool { return len(passwordErrors(p)) == 0 }
