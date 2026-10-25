package metadata_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/meme-radar/radar/internal/metadata"
)

func TestSocialsNormalization(t *testing.T) {
	for _, tc := range []struct {
		name, input string
		want        map[string]string
	}{
		{"usernames", `{"twitter":"@alice_1","telegram":"room_2"}`, map[string]string{"twitter": "https://x.com/alice_1", "telegram": "https://t.me/room_2"}},
		{"merge", `{"website":"https://project.example","extensions":{"x":"@project_x","github":"https://github.com/project"},"links":{"telegram":"@project_chat"}}`, map[string]string{"website": "https://project.example", "twitter": "https://x.com/project_x", "github": "https://github.com/project", "telegram": "https://t.me/project_chat"}},
		{"link_array", `{"links":[{"type":"twitter","url":"https://x.com/project"},{"name":"website","url":"https://project.example"}]}`, map[string]string{"twitter": "https://x.com/project", "website": "https://project.example"}},
		{"unsafe", `{"twitter":"javascript:alert(1)","telegram":"file:///tmp/a","website":"ipfs://bafkxxx","github":"http://localhost/a","image":"javascript:x"}`, map[string]string{}},
		{"wrong_types", `{"twitter":true,"website":[],"extensions":42,"links":null}`, map[string]string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := metadata.ParseMetadata([]byte(tc.input), "https://gateway.pinata.cloud/ipfs/")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Socials, tc.want) {
				t.Errorf("socials=%v; want %v", got.Socials, tc.want)
			}
		})
	}
	t.Run("limits_and_names", func(t *testing.T) {
		// 20 characters in the URL prefix + 236 path characters = 256.
		raw, _ := json.Marshal(map[string]string{"website": "https://example.com/" + strings.Repeat("界", 300), "description": strings.Repeat("🙂", 1001), "name": " A\x00\nB ", "symbol": strings.Repeat("中", 33)})
		got, err := metadata.ParseMetadata(raw, "https://gateway.pinata.cloud/ipfs/")
		if err != nil {
			t.Fatal(err)
		}
		if got.Socials["website"] != "https://example.com/"+strings.Repeat("界", 236) {
			t.Errorf("URL length=%d; want 256 characters", len([]rune(got.Socials["website"])))
		}
		if got.Description != strings.Repeat("🙂", 1000) {
			t.Errorf("description length=%d; want 1000 characters", len([]rune(got.Description)))
		}
		if got.Name != "AB" || got.Symbol != strings.Repeat("中", 32) {
			t.Errorf("D103 name=%q symbol length=%d", got.Name, len([]rune(got.Symbol)))
		}
	})
	t.Run("image_alias_ipfs", func(t *testing.T) {
		got, err := metadata.ParseMetadata([]byte(`{"image_url":"ipfs://QmezC5igZgKXgZ4nDRd2c1jsJ4DSTz3egw9WX1PyFbomVr"}`), "https://gateway.pinata.cloud/ipfs/")
		if err != nil || got.ImageURL != "https://gateway.pinata.cloud/ipfs/QmezC5igZgKXgZ4nDRd2c1jsJ4DSTz3egw9WX1PyFbomVr" {
			t.Errorf("image=%q err=%v", got.ImageURL, err)
		}
	})
}
