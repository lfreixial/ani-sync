// Package matching handles human-confirmed source queries, never guessed IDs.
package matching

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/lfreixial/ani-sync/internal/anilist"
	"github.com/lfreixial/ani-sync/internal/config"
)

func ValidateQuery(query string) error {
	if !utf8.ValidString(query) || strings.TrimSpace(query) == "" || len(query) > 300 {
		return errors.New("source title must contain 1–300 UTF-8 bytes")
	}
	if strings.HasPrefix(strings.TrimSpace(query), "-") {
		return errors.New("source title must not begin with '-' (ani-cli interprets options anywhere)")
	}
	for _, r := range query {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return errors.New("source title contains control characters")
		}
	}
	return nil
}
func normalize(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}
func Matches(media anilist.Media, query string) bool {
	if query == "" {
		return true
	}
	q := normalize(query)
	if q == "" {
		return false
	}
	titles := append([]string{media.Title.English, media.Title.Romaji, media.Title.Native}, media.Synonyms...)
	for _, title := range titles {
		if strings.Contains(normalize(title), q) {
			return true
		}
	}
	return false
}
func Filter(entries []anilist.Entry, query string) []anilist.Entry {
	result := []anilist.Entry{}
	for _, e := range entries {
		if Matches(e.Media, query) {
			result = append(result, e)
		}
	}
	return result
}

type Aliases map[string]string

func Load(s *config.Store) (Aliases, error) {
	a := Aliases{}
	err := s.Read("mappings.json", &a)
	if errors.Is(err, os.ErrNotExist) {
		err = nil
	}
	if err != nil {
		return nil, err
	}
	if a == nil {
		a = Aliases{}
	}
	for k, v := range a {
		n, e := strconv.Atoi(k)
		if e != nil || n <= 0 || ValidateQuery(v) != nil {
			return nil, errors.New("invalid saved title mapping")
		}
	}
	return a, nil
}
func (a Aliases) Get(id int) string { return a[strconv.Itoa(id)] }
func (a Aliases) Save(s *config.Store, id int, query string) error {
	if id <= 0 {
		return errors.New("invalid mapping ID")
	}
	if err := ValidateQuery(query); err != nil {
		return err
	}
	a[strconv.Itoa(id)] = query
	return s.Write("mappings.json", a)
}
