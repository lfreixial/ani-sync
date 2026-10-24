package matching

import (
	"path/filepath"
	"testing"

	"github.com/lfreixial/ani-sync/internal/anilist"
	"github.com/lfreixial/ani-sync/internal/config"
)

func TestTitlesAndAliases(t *testing.T) {
	media := anilist.Media{ID: 9, Title: anilist.Title{English: "Frieren: Beyond Journey's End", Romaji: "Sousou no Frieren", Native: "葬送のフリーレン"}, Synonyms: []string{"Frieren Season 1"}}
	for _, q := range []string{"frieren", "sousou", "葬送", "season-1"} {
		if !Matches(media, q) {
			t.Error(q)
		}
	}
	if Matches(media, "second season") || Matches(media, "!!!") {
		t.Fatal("unrelated title matched")
	}
	s, err := config.Open(filepath.Join(t.TempDir(), "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err := Load(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Save(s, 9, "Sousou no Frieren"); err != nil {
		t.Fatal(err)
	}
	a, err = Load(s)
	if err != nil || a.Get(9) != "Sousou no Frieren" {
		t.Fatal(a, err)
	}
}
func TestSourceInput(t *testing.T) {
	for _, q := range []string{"", "--update", " -U", "title\n--update", "a\x1b[31m", "a\u202eb"} {
		if ValidateQuery(q) == nil {
			t.Errorf("accepted %q", q)
		}
	}
	for _, q := range []string{"Frieren", "葬送のフリーレン", "a;$(touch nope)`id`", "foo --update"} {
		if err := ValidateQuery(q); err != nil {
			t.Errorf("%q: %v", q, err)
		}
	}
}
