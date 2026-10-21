package activity

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

const ConfigPath = "data/activity-config.json"
const DataPath = "data/public-activity.json"
const ExclusionsPath = "data/activity-excluded.json"

func Hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func Decode(b []byte, out any) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		return errors.New("JSON cannot be null")
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("JSON has trailing data")
	}
	return nil
}
func Encode(v any) []byte {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(b, '\n')
}
func Block(readme string) (string, error) {
	if strings.Count(readme, Start) != 1 || strings.Count(readme, End) != 1 {
		return "", errors.New("README needs exactly one activity marker pair")
	}
	a, b := strings.Index(readme, Start)+len(Start), strings.Index(readme, End)
	if b < a {
		return "", errors.New("README markers are reversed")
	}
	return strings.TrimSpace(readme[a:b]), nil
}
func Build(c Config, old Data, excluded []Exclusion, readme string, incoming []Entry, now time.Time) (Data, string, error) {
	if err := c.ValidateEntries(old.Entries); err != nil {
		return Data{}, "", err
	}
	if err := c.ValidateEntries(incoming); err != nil {
		return Data{}, "", err
	}
	block, err := Block(readme)
	if err != nil {
		return Data{}, "", err
	}
	if old.ReadmeSHA != "" && old.ReadmeSHA != Hash([]byte(block)) {
		return Data{}, "", errors.New("generated README block was edited; preserve it and edit activity JSON instead")
	}
	data, err := Merge(old, incoming, excluded, now)
	if err != nil {
		return Data{}, "", err
	}
	content, err := c.Render(data)
	if err != nil {
		return Data{}, "", err
	}
	data.ReadmeSHA = Hash([]byte(strings.TrimSpace(content)))
	next, err := ReplaceBlock(readme, content)
	if err != nil {
		return Data{}, "", err
	}
	return data, next, nil
}
func Check(c Config, data Data, excluded []Exclusion, readme string) error {
	if data.Version != 1 || data.Updated.IsZero() {
		return errors.New("invalid activity data version or date")
	}
	if err := c.ValidateEntries(data.Entries); err != nil {
		return err
	}
	if _, err := Merge(data, nil, excluded, data.Updated); err != nil {
		return err
	}
	for _, x := range excluded {
		for _, e := range data.Entries {
			if x.ID == e.ID {
				return errors.New("excluded activity is still published")
			}
		}
	}
	content, err := c.Render(data)
	if err != nil {
		return err
	}
	block, err := Block(readme)
	if err != nil {
		return err
	}
	if strings.TrimSpace(content) != block || Hash([]byte(block)) != data.ReadmeSHA {
		return fmt.Errorf("README and activity JSON differ")
	}
	return nil
}
