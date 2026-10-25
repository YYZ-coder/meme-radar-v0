package metadata

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
)

// ErrDisabled means an operator disabled this source by clearing its URL.
var ErrDisabled = errors.New("metadata: source disabled")

func (f *Fetcher) fourMemeURI(address []byte) (string, error) {
	if f.options.FourMemeAPIURL == "" {
		return "", ErrDisabled
	}
	if len(address) != 20 {
		return "", ErrNoSource
	}
	uri, err := url.Parse(f.options.FourMemeAPIURL)
	if err != nil {
		return "", err
	}
	query := uri.Query()
	query.Set("address", "0x"+hex.EncodeToString(address))
	uri.RawQuery = query.Encode()
	return uri.String(), nil
}

// FetchFourMeme reads Four.meme's optional unofficial display metadata API.
func (f *Fetcher) FetchFourMeme(ctx context.Context, address []byte) (Document, error) {
	uri, err := f.fourMemeURI(address)
	if err != nil {
		return Document{}, err
	}
	if err := f.limits.waitFour(ctx); err != nil {
		return Document{}, err
	}
	data, err := f.read(ctx, uri)
	if err != nil {
		return Document{}, err
	}
	var envelope struct {
		Code *int
		Data map[string]json.RawMessage
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Document{}, err
	}
	if envelope.Code == nil || *envelope.Code != 0 {
		return Document{}, fmt.Errorf("metadata: Four.meme API error")
	}
	if envelope.Data == nil {
		return Document{}, ErrNoSource
	}
	obj := envelope.Data
	for target, source := range map[string]string{"symbol": "shortName", "description": "descr", "website": "webUrl", "telegram": "telegramUrl", "twitter": "twitterUrl"} {
		obj[target] = obj[source]
	}
	mapped, err := json.Marshal(obj)
	if err != nil {
		return Document{}, err
	}
	doc, err := ParseMetadata(mapped, f.gateway())
	if err != nil {
		return Document{}, err
	}
	keepRaw(&doc, data)
	return doc, nil
}
