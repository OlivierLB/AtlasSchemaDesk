// Package snapshot writes files atomically and computes stable schema
// fingerprints, identical to the historical Python implementation.
package snapshot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/OlivierLB/AtlasSchemaDesk/internal/model"
)

// WriteAtomic replaces path with data through a temporary file in the same
// directory (mode 0600), so readers never see a partial file.
func WriteAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".")
	if err != nil {
		return err
	}
	temporary := file.Name()
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = os.Rename(temporary, path)
	}
	if writeErr != nil {
		_ = os.Remove(temporary)
	}
	return writeErr
}

// WriteJSON encodes value compactly and writes it atomically.
func WriteJSON(path string, value any) error {
	data, err := model.Marshal(value)
	if err != nil {
		return err
	}
	return WriteAtomic(path, data)
}

// SourceHash is the first 16 hex digits of the SHA-256 of the canonical JSON
// (sorted keys, compact, non-ASCII kept) of a source's tables.
func SourceHash(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var generic any
	if err := decoder.Decode(&generic); err != nil {
		return "", err
	}
	var buffer bytes.Buffer
	if err := canonical(&buffer, generic); err != nil {
		return "", err
	}
	digest := sha256.Sum256(buffer.Bytes())
	return hex.EncodeToString(digest[:])[:16], nil
}

func canonical(buffer *bytes.Buffer, value any) error {
	switch v := value.(type) {
	case nil:
		buffer.WriteString("null")
	case bool:
		buffer.WriteString(strconv.FormatBool(v))
	case json.Number:
		buffer.WriteString(v.String())
	case string:
		pythonString(buffer, v)
	case []any:
		buffer.WriteByte('[')
		for index, item := range v {
			if index > 0 {
				buffer.WriteByte(',')
			}
			if err := canonical(buffer, item); err != nil {
				return err
			}
		}
		buffer.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys) // byte order of UTF-8 equals Python's code point order
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			pythonString(buffer, key)
			buffer.WriteByte(':')
			if err := canonical(buffer, v[key]); err != nil {
				return err
			}
		}
		buffer.WriteByte('}')
	default:
		return fmt.Errorf("unsupported canonical JSON value %T", value)
	}
	return nil
}

// pythonString escapes like json.dumps(ensure_ascii=False).
func pythonString(buffer *bytes.Buffer, text string) {
	buffer.WriteByte('"')
	for _, r := range text {
		switch r {
		case '"':
			buffer.WriteString(`\"`)
		case '\\':
			buffer.WriteString(`\\`)
		case '\n':
			buffer.WriteString(`\n`)
		case '\r':
			buffer.WriteString(`\r`)
		case '\t':
			buffer.WriteString(`\t`)
		case '\b':
			buffer.WriteString(`\b`)
		case '\f':
			buffer.WriteString(`\f`)
		default:
			if r < 0x20 {
				fmt.Fprintf(buffer, `\u%04x`, r)
			} else {
				buffer.WriteRune(r)
			}
		}
	}
	buffer.WriteByte('"')
}
