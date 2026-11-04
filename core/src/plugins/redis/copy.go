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

package redis

import (
	"fmt"

	"github.com/clidey/whodb/core/src/engine"
)

// CopyStorageUnit copies a Redis key (and its value/TTL) from sourceConfig to targetConfig.
func (p *RedisPlugin) CopyStorageUnit(
	sourceConfig *engine.PluginConfig,
	_ string,
	targetConfig *engine.PluginConfig,
	_ string,
	storageUnit string,
	includeData bool,
	existingStrategy engine.CopyExistingStrategy,
) (*engine.CopyTableDetail, error) {
	sourceClient, err := DB(sourceConfig)
	if err != nil {
		return nil, err
	}
	defer sourceClient.Close()

	targetClient, err := DB(targetConfig)
	if err != nil {
		return nil, err
	}
	defer targetClient.Close()

	sourceCtx := sourceConfig.OperationContext()
	targetCtx := targetConfig.OperationContext()

	existsCount, err := sourceClient.Exists(sourceCtx, storageUnit).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect source key %s: %w", storageUnit, err)
	}
	if existsCount == 0 {
		return nil, fmt.Errorf("source key %q does not exist", storageUnit)
	}

	targetExistsCount, err := targetClient.Exists(targetCtx, storageUnit).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to inspect target key %s: %w", storageUnit, err)
	}
	targetExists := targetExistsCount > 0

	if targetExists {
		switch existingStrategy {
		case engine.CopyExistingStrategySkip:
			return &engine.CopyTableDetail{
				Name:       storageUnit,
				Status:     engine.CopyTableStatusSkipped,
				RowsCopied: 0,
				Message:    "Skipped existing key",
			}, nil
		case engine.CopyExistingStrategyOverwrite:
			if err := targetClient.Del(targetCtx, storageUnit).Err(); err != nil {
				return nil, fmt.Errorf("failed to delete existing target key %s: %w", storageUnit, err)
			}
		case engine.CopyExistingStrategyAppend:
			// Merge/append elements into the existing key where supported.
		}
	}

	if !includeData {
		return &engine.CopyTableDetail{
			Name:       storageUnit,
			Status:     engine.CopyTableStatusSuccess,
			RowsCopied: 0,
		}, nil
	}

	keyType, err := sourceClient.Type(sourceCtx, storageUnit).Result()
	if err != nil {
		return nil, fmt.Errorf("failed to determine Redis key type for %s: %w", storageUnit, err)
	}

	pttl, _ := sourceClient.PTTL(sourceCtx, storageUnit).Result()
	rowsCopied := 0

	switch keyType {
	case "string":
		val, err := sourceClient.Get(sourceCtx, storageUnit).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to read string key %s: %w", storageUnit, err)
		}
		if err := targetClient.Set(targetCtx, storageUnit, val, 0).Err(); err != nil {
			return nil, fmt.Errorf("failed to write string key %s: %w", storageUnit, err)
		}
		rowsCopied = 1
	case "hash":
		fields, err := sourceClient.HGetAll(sourceCtx, storageUnit).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to read hash key %s: %w", storageUnit, err)
		}
		if len(fields) > 0 {
			args := make([]any, 0, len(fields)*2)
			for k, v := range fields {
				args = append(args, k, v)
			}
			if err := targetClient.HSet(targetCtx, storageUnit, args...).Err(); err != nil {
				return nil, fmt.Errorf("failed to write hash key %s: %w", storageUnit, err)
			}
			rowsCopied = len(fields)
		}
	case "list":
		values, err := sourceClient.LRange(sourceCtx, storageUnit, 0, -1).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to read list key %s: %w", storageUnit, err)
		}
		if len(values) > 0 {
			args := make([]any, len(values))
			for i, v := range values {
				args[i] = v
			}
			if err := targetClient.RPush(targetCtx, storageUnit, args...).Err(); err != nil {
				return nil, fmt.Errorf("failed to write list key %s: %w", storageUnit, err)
			}
			rowsCopied = len(values)
		}
	case "set":
		members, err := sourceClient.SMembers(sourceCtx, storageUnit).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to read set key %s: %w", storageUnit, err)
		}
		if len(members) > 0 {
			args := make([]any, len(members))
			for i, v := range members {
				args[i] = v
			}
			if err := targetClient.SAdd(targetCtx, storageUnit, args...).Err(); err != nil {
				return nil, fmt.Errorf("failed to write set key %s: %w", storageUnit, err)
			}
			rowsCopied = len(members)
		}
	case "zset":
		members, err := sourceClient.ZRangeWithScores(sourceCtx, storageUnit, 0, -1).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to read zset key %s: %w", storageUnit, err)
		}
		if len(members) > 0 {
			if err := targetClient.ZAdd(targetCtx, storageUnit, members...).Err(); err != nil {
				return nil, fmt.Errorf("failed to write zset key %s: %w", storageUnit, err)
			}
			rowsCopied = len(members)
		}
	default:
		dumpVal, err := sourceClient.Dump(sourceCtx, storageUnit).Result()
		if err != nil {
			return nil, fmt.Errorf("failed to dump Redis key %s: %w", storageUnit, err)
		}
		ttl := pttl
		if ttl < 0 {
			ttl = 0
		}
		if err := targetClient.RestoreReplace(targetCtx, storageUnit, ttl, dumpVal).Err(); err != nil {
			return nil, fmt.Errorf("failed to restore Redis key %s: %w", storageUnit, err)
		}
		rowsCopied = 1
	}

	if pttl > 0 {
		_ = targetClient.PExpire(targetCtx, storageUnit, pttl).Err()
	}

	return &engine.CopyTableDetail{
		Name:       storageUnit,
		Status:     engine.CopyTableStatusSuccess,
		RowsCopied: rowsCopied,
	}, nil
}
