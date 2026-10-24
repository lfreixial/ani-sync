package config

import (
	"errors"
	"os"
	"strconv"
)

type Config struct {
	ClientID      string `json:"client_id"`
	TitleLanguage string `json:"title_language"`
}

func (c Config) Validate() error {
	if c.ClientID != "" {
		n, err := strconv.Atoi(c.ClientID)
		if err != nil || n <= 0 {
			return errors.New("client_id must be a positive integer")
		}
	}
	switch c.TitleLanguage {
	case "english", "romaji", "native":
	default:
		return errors.New("title_language must be english, romaji, or native")
	}
	return nil
}

func Load(s *Store) (Config, error) {
	c := Config{TitleLanguage: "english"}
	err := s.Read("config.json", &c)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return c, err
	}
	return c, c.Validate()
}
