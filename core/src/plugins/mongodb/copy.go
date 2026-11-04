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

package mongodb

import (
	"fmt"
	"strings"

	"github.com/clidey/whodb/core/src/engine"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func resolveMongoDatabase(config *engine.PluginConfig, schema string) string {
	if s := strings.TrimSpace(schema); s != "" {
		return s
	}
	if config != nil && config.Credentials != nil {
		return strings.TrimSpace(config.Credentials.Database)
	}
	return ""
}

// CopyStorageUnit copies a MongoDB collection (structure and optionally documents)
// from sourceConfig/sourceSchema to targetConfig/targetSchema.
func (p *MongoDBPlugin) CopyStorageUnit(
	sourceConfig *engine.PluginConfig,
	sourceSchema string,
	targetConfig *engine.PluginConfig,
	targetSchema string,
	storageUnit string,
	includeData bool,
	existingStrategy engine.CopyExistingStrategy,
) (*engine.CopyTableDetail, error) {
	sourceDBName := resolveMongoDatabase(sourceConfig, sourceSchema)
	targetDBName := resolveMongoDatabase(targetConfig, targetSchema)
	if sourceDBName == "" {
		return nil, fmt.Errorf("source MongoDB database is required")
	}
	if targetDBName == "" {
		return nil, fmt.Errorf("target MongoDB database is required")
	}

	sourceExists, err := p.StorageUnitExists(sourceConfig, sourceDBName, storageUnit)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect source collection %s: %w", storageUnit, err)
	}
	if !sourceExists {
		return nil, fmt.Errorf("source collection %q does not exist", storageUnit)
	}

	targetExists, err := p.StorageUnitExists(targetConfig, targetDBName, storageUnit)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect target collection %s: %w", storageUnit, err)
	}

	sourceCtx, sourceCancel := opCtx(sourceConfig)
	defer sourceCancel()
	targetCtx, targetCancel := opCtx(targetConfig)
	defer targetCancel()

	sourceClient, err := DB(sourceConfig)
	if err != nil {
		return nil, err
	}
	defer disconnectClient(sourceClient)

	targetClient, err := DB(targetConfig)
	if err != nil {
		return nil, err
	}
	defer disconnectClient(targetClient)

	sourceDB := sourceClient.Database(sourceDBName)
	targetDB := targetClient.Database(targetDBName)

	if targetExists {
		switch existingStrategy {
		case engine.CopyExistingStrategySkip:
			return &engine.CopyTableDetail{
				Name:       storageUnit,
				Status:     engine.CopyTableStatusSkipped,
				RowsCopied: 0,
				Message:    "Skipped existing collection",
			}, nil
		case engine.CopyExistingStrategyOverwrite:
			if err := targetDB.Collection(storageUnit).Drop(targetCtx); err != nil {
				return nil, fmt.Errorf("failed to drop existing target collection %s: %w", storageUnit, err)
			}
			targetExists = false
		case engine.CopyExistingStrategyAppend:
			// Keep existing target collection and append documents below.
		}
	}

	if !targetExists {
		if err := targetDB.CreateCollection(targetCtx, storageUnit); err != nil && !isNamespaceExistsError(err) {
			return nil, fmt.Errorf("failed to create target collection %s: %w", storageUnit, handleMongoError(err))
		}
	}

	if !includeData {
		return &engine.CopyTableDetail{
			Name:       storageUnit,
			Status:     engine.CopyTableStatusSuccess,
			RowsCopied: 0,
		}, nil
	}

	cursor, err := sourceDB.Collection(storageUnit).Find(sourceCtx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("failed to read documents from %s: %w", storageUnit, handleMongoError(err))
	}
	defer cursor.Close(sourceCtx)

	const batchSize = 500
	batch := make([]any, 0, batchSize)
	totalCopied := 0
	targetCollection := targetDB.Collection(storageUnit)
	insertOpts := options.InsertMany().SetOrdered(existingStrategy != engine.CopyExistingStrategyAppend)

	flushBatch := func() error {
		if len(batch) == 0 {
			return nil
		}
		res, err := targetCollection.InsertMany(targetCtx, batch, insertOpts)
		if err != nil {
			if existingStrategy == engine.CopyExistingStrategyAppend && mongo.IsDuplicateKeyError(err) {
				if res != nil {
					totalCopied += len(res.InsertedIDs)
				}
				batch = batch[:0]
				return nil
			}
			return fmt.Errorf("failed to insert documents into %s: %w", storageUnit, handleMongoError(err))
		}
		totalCopied += len(res.InsertedIDs)
		batch = batch[:0]
		return nil
	}

	for cursor.Next(sourceCtx) {
		var doc bson.D
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("failed to decode document from %s: %w", storageUnit, err)
		}
		batch = append(batch, doc)
		if len(batch) >= batchSize {
			if err := flushBatch(); err != nil {
				return nil, err
			}
		}
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor error reading %s: %w", storageUnit, err)
	}
	if err := flushBatch(); err != nil {
		return nil, err
	}

	return &engine.CopyTableDetail{
		Name:       storageUnit,
		Status:     engine.CopyTableStatusSuccess,
		RowsCopied: totalCopied,
	}, nil
}
