package metadata

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/meme-radar/radar/internal/solana"
)

var metaplexProgram = solana.MustKey("metaqbxxUerdq28cj1RbAWkYQm3ybzjb6a8bt518x1s")

// Metaplex contains the MetadataV1 prefix needed to find token JSON.
type Metaplex struct {
	Mint              solana.Key
	Name, Symbol, URI string
}

// MetaplexPDA derives the token metadata account of mint.
func MetaplexPDA(mint solana.Key) (solana.Key, error) {
	pda, _, err := solana.FindProgramAddress([][]byte{[]byte("metadata"), metaplexProgram[:], mint[:]}, metaplexProgram)
	return pda, err
}

// DecodeMetaplexMetadata decodes a Borsh MetadataV1 account prefix.
func DecodeMetaplexMetadata(data []byte) (Metaplex, error) {
	if len(data) < 65 || data[0] != 4 {
		return Metaplex{}, fmt.Errorf("metadata: invalid MetadataV1 prefix")
	}
	meta := Metaplex{Mint: solana.Key(data[33:65])}
	fields := []*string{&meta.Name, &meta.Symbol, &meta.URI}
	rest := data[65:]
	for _, field := range fields {
		if len(rest) < 4 {
			return Metaplex{}, fmt.Errorf("metadata: truncated Borsh length")
		}
		size := uint64(binary.LittleEndian.Uint32(rest))
		rest = rest[4:]
		if size > uint64(len(rest)) {
			return Metaplex{}, fmt.Errorf("metadata: truncated Borsh string")
		}
		*field = strings.TrimSpace(strings.TrimRight(string(rest[:size]), "\x00"))
		rest = rest[size:]
	}
	meta.Name = cleanName(meta.Name, 128)
	meta.Symbol = cleanName(meta.Symbol, 32)
	return meta, nil
}
