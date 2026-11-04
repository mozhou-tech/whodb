/*
 * Copyright 2026 Clidey, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Package opsplane integrates environments and databases managed by ops-control-plane into WhoDB.
package opsplane

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
)

const (
	defaultEnvironmentConfigSecretMaterial = "1dc76a59-9684-4b56-b5d4-56297b4743a2"
	gcmNonceLength                         = 12
	gcmTagLength                           = 16
	aes256KeyLength                        = 32
)

// DecryptEnvironmentSecret decrypts a secret value stored by ops-control-plane's
// encrypted-text codec ([12-byte iv][ciphertext][16-byte tag] in base64).
// If the input is not encrypted or cannot be decrypted, the raw input is returned.
func DecryptEnvironmentSecret(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	raw, err := base64.StdEncoding.DecodeString(trimmed)
	if err != nil || len(raw) < gcmNonceLength+gcmTagLength {
		return value
	}

	secretMaterial := strings.TrimSpace(os.Getenv("ENVIRONMENT_CONFIG_SECRET_KEY"))
	if secretMaterial == "" {
		secretMaterial = defaultEnvironmentConfigSecretMaterial
	}
	key := sha256.Sum256([]byte(secretMaterial))

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return value
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return value
	}

	nonce := raw[:gcmNonceLength]
	ciphertextWithTag := raw[gcmNonceLength:]
	plaintext, err := gcm.Open(nil, nonce, ciphertextWithTag, nil)
	if err != nil {
		return value
	}
	return string(plaintext)
}

// ParseProjectEnvKeyring parses PROJECT_ENV_ENCRYPTION_KEYS and PROJECT_ENV_MASTER_KEY
// into a map of keyID -> 32-byte AES key.
func ParseProjectEnvKeyring(envMap map[string]string) map[string][]byte {
	keyring := make(map[string][]byte)

	rawJSON := strings.TrimSpace(envMap["PROJECT_ENV_ENCRYPTION_KEYS"])
	if rawJSON == "" {
		rawJSON = strings.TrimSpace(os.Getenv("PROJECT_ENV_ENCRYPTION_KEYS"))
	}
	if rawJSON != "" {
		var parsed map[string]string
		if err := json.Unmarshal([]byte(rawJSON), &parsed); err == nil {
			for k, v := range parsed {
				if decoded, err := decodeMasterKey(v); err == nil {
					keyring[strings.TrimSpace(k)] = decoded
				}
			}
		}
	}

	if len(keyring) == 0 {
		rawMaster := strings.TrimSpace(envMap["PROJECT_ENV_MASTER_KEY"])
		if rawMaster == "" {
			rawMaster = strings.TrimSpace(os.Getenv("PROJECT_ENV_MASTER_KEY"))
		}
		if rawMaster != "" {
			if decoded, err := decodeMasterKey(rawMaster); err == nil {
				keyID := strings.TrimSpace(envMap["PROJECT_ENV_ACTIVE_KEY_ID"])
				if keyID == "" {
					keyID = "v1"
				}
				keyring[keyID] = decoded
			}
		}
	}

	return keyring
}

func decodeMasterKey(raw string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.RawURLEncoding} {
		if decoded, err := enc.DecodeString(trimmed); err == nil && len(decoded) == aes256KeyLength {
			return decoded, nil
		}
	}
	return nil, errors.New("invalid 32-byte master key")
}

// DecryptProjectEnvVersion decrypts an envelope-encrypted .env file version from project_env_versions.
// ops-control-plane packs payloads as [12-byte iv][16-byte tag][ciphertext].
func DecryptProjectEnvVersion(
	keyring map[string][]byte,
	envSlug string,
	fileID string,
	revision int,
	keyID string,
	wrappedKey []byte,
	encryptedContent []byte,
) (string, error) {
	masterKey, ok := keyring[strings.TrimSpace(keyID)]
	if !ok {
		return "", fmt.Errorf("missing master key for key_id %q", keyID)
	}

	keyAAD, err := json.Marshal([]any{"project-env/v1", envSlug, fileID, revision, "key:" + keyID})
	if err != nil {
		return "", err
	}
	dataKey, err := openProjectEnvEnvelope(wrappedKey, masterKey, keyAAD)
	if err != nil {
		return "", fmt.Errorf("unwrap project env data key: %w", err)
	}

	contentAAD, err := json.Marshal([]any{"project-env/v1", envSlug, fileID, revision, "content"})
	if err != nil {
		return "", err
	}
	plaintext, err := openProjectEnvEnvelope(encryptedContent, dataKey, contentAAD)
	if err != nil {
		return "", fmt.Errorf("decrypt project env content: %w", err)
	}
	return string(plaintext), nil
}

func openProjectEnvEnvelope(payload []byte, key []byte, aad []byte) ([]byte, error) {
	if len(payload) < gcmNonceLength+gcmTagLength {
		return nil, errors.New("encrypted payload is truncated")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	iv := payload[:gcmNonceLength]
	tag := payload[gcmNonceLength : gcmNonceLength+gcmTagLength]
	ciphertext := payload[gcmNonceLength+gcmTagLength:]

	ciphertextWithTag := make([]byte, 0, len(ciphertext)+len(tag))
	ciphertextWithTag = append(ciphertextWithTag, slices.Clone(ciphertext)...)
	ciphertextWithTag = append(ciphertextWithTag, tag...)

	return gcm.Open(nil, iv, ciphertextWithTag, aad)
}
