//go:build !arm && !riscv64

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

package graph

import (
	stdctx "context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/clidey/whodb/core/baml_client"
	"github.com/clidey/whodb/core/baml_client/types"
	"github.com/clidey/whodb/core/graph/model"
	"github.com/clidey/whodb/core/src/bamlconfig"
	"github.com/clidey/whodb/core/src/envconfig"
	"github.com/clidey/whodb/core/src/log"
	"github.com/clidey/whodb/core/src/source"
	"github.com/clidey/whodb/core/src/sqlguard"
)

func init() {
	RegisterAIChatStreamHandler(ceAIChatStreamHandler)
}

func ceAIChatStreamHandler(w http.ResponseWriter, r *http.Request) {
	log.Debugf("AI Chat Stream: Handler started")

	// Parse request
	req, err := ParseStreamRequest(r)
	if err != nil {
		log.Debugf("AI Chat Stream: ParseStreamRequest failed: %v", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	log.Debugf("AI Chat Stream: Request parsed - model=%s, ref=%+v, query=%s", req.ModelType, req.Ref, req.Input.Query)

	// Setup SSE
	flusher := SetupSSEHeaders(w)
	if flusher == nil {
		http.Error(w, "Streaming unsupported", http.StatusInternalServerError)
		return
	}
	chatContext, cancel := stdctx.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	spec, session, err := getSourceSessionForContext(chatContext)
	if err != nil {
		log.Debugf("AI Chat Stream: Failed to create source session: %v", err)
		SendSSEError(w, flusher, "No source session available")
		return
	}
	auditScope := sourceAuditScopeFromContext(r.Context(), spec)
	queryRunner, ok := source.AsReadOnlyQueryRunner(auditScope, session)
	if !ok {
		SendSSEError(w, flusher, "Source queries are not supported")
		return
	}

	// Build ExternalModel, resolving credentials from environment if providerId is set
	creds := envconfig.ResolveProviderCredentials(req.ProviderId, req.Token, req.Endpoint, req.ModelType)
	modelConfig := &source.ExternalModel{
		Type:     creds.ModelType,
		Token:    creds.Token,
		Model:    req.Model,
		Endpoint: creds.Endpoint,
	}

	SendSSEProgressDetail(w, flusher, "schema", "started", "正在扫描当前数据库的表与字段定义...")
	// Build object details for the selected chat scope.
	log.Debugf("AI Chat Stream: Building object details for ref=%+v", req.Ref)
	resolvedRef := sourceRefFromInput(req.Ref)
	var schemaSummary string
	tableDetails, err := BuildObjectDetailsWithProgress(
		chatContext,
		auditScope,
		session,
		resolvedRef,
		spec.Contract.DefaultObjectKind,
		func(completed, total int, _ string, summary string) {
			schemaSummary = summary
			if completed < total {
				SendSSEProgressDetail(w, flusher, "schema", "started", summary)
			}
		},
	)
	if err != nil {
		log.Debugf("AI Chat Stream: BuildObjectDetails failed: %v", err)
		SendSSEError(w, flusher, "Failed to get table info: "+err.Error())
		return
	}
	log.Debugf("AI Chat Stream: Table details built, length=%d", len(tableDetails))
	SendSSEProgressDetail(w, flusher, "schema", "completed", schemaSummary)

	scope := sourceScopeForChat(spec, resolvedRef)

	// Setup BAML context
	dbContext := types.DatabaseContext{
		Database_type:         spec.ID,
		Schema:                scope,
		Tables_and_fields:     tableDetails,
		Previous_conversation: req.Input.PreviousConversation,
	}
	log.Debugf("AI Chat Stream: BAML context created")

	callOpts := bamlconfig.SetupAIClient(modelConfig)
	runChatLoop(chatContext, w, flusher, dbContext, req.Input.Query, callOpts, queryRunner)
}

func runChatLoop(
	ctx stdctx.Context,
	w http.ResponseWriter,
	flusher http.Flusher,
	dbContext types.DatabaseContext,
	userQuery string,
	callOpts []baml_client.CallOptionFunc,
	queryRunner source.ReadOnlyQueryRunner,
) {
	request := userQuery
	var lastFailedSQL, lastQueryError string
	for attempt := range 2 {
		SendSSEProgressDetail(w, flusher, "plan", "started", "正在调用模型分析表结构与问题，生成思考与查询方案...")
		stream, err := baml_client.Stream.GenerateSQLQuery(ctx, dbContext, request, callOpts...)
		if err != nil {
			sendModelError(w, flusher, err.Error())
			return
		}
		var responses *[]types.ChatResponse
		var lastStreamedThought, lastStreamedDetail string
		for chunk := range stream {
			if chunk.IsError {
				sendModelError(w, flusher, chunk.Error.Error())
				return
			}
			if chunk.IsFinal {
				responses = chunk.Final()
				break
			}
			if partial := chunk.Stream(); partial != nil {
				var currentThought, currentSQL strings.Builder
				for _, item := range *partial {
					if item.Text == nil || *item.Text == "" {
						continue
					}
					itemType := "message"
					if item.Type != nil {
						itemType = bamlconfig.ConvertBAMLTypeToWhoDB(*item.Type)
					}
					if itemType == "sql" {
						if currentSQL.Len() > 0 {
							currentSQL.WriteString("\n")
						}
						currentSQL.WriteString(*item.Text)
					} else if itemType == "message" || itemType == "text" {
						if currentThought.Len() > 0 {
							currentThought.WriteString("\n\n")
						}
						currentThought.WriteString(*item.Text)
					}
				}
				thoughtStr := strings.TrimSpace(currentThought.String())
				sqlStr := strings.TrimSpace(currentSQL.String())
				if thoughtStr != "" && thoughtStr != lastStreamedThought {
					lastStreamedThought = thoughtStr
					SendSSEChunk(w, flusher, "message", thoughtStr)
				}
				detail := thoughtStr
				if sqlStr != "" {
					if detail != "" {
						detail += "\n\nSQL:\n" + sqlStr
					} else {
						detail = "SQL:\n" + sqlStr
					}
				}
				if detail != "" && detail != lastStreamedDetail {
					lastStreamedDetail = detail
					SendSSEProgressDetail(w, flusher, "plan", "started", detail)
				}
			}
		}
		if err := ctx.Err(); err != nil {
			if err == stdctx.DeadlineExceeded {
				SendSSEError(w, flusher, "The chat request timed out. Please try again.")
			}
			return
		}
		if responses == nil || len(*responses) == 0 {
			SendSSEProgressDetail(w, flusher, "plan", "completed", lastStreamedDetail)
			SendSSEError(w, flusher, "The model returned no answer")
			return
		}

		var finalThoughts, finalSQLs []string
		for _, r := range *responses {
			txt := strings.TrimSpace(r.Text)
			if txt == "" {
				continue
			}
			if r.Type == types.ChatMessageTypeSQL {
				finalSQLs = append(finalSQLs, txt)
			} else if r.Type == types.ChatMessageTypeMESSAGE {
				finalThoughts = append(finalThoughts, txt)
			}
		}
		planSummary := strings.Join(finalThoughts, "\n\n")
		if len(finalSQLs) > 0 {
			sqlBlock := "生成 SQL:\n" + strings.Join(finalSQLs, "\n")
			if planSummary != "" {
				planSummary += "\n\n" + sqlBlock
			} else {
				planSummary = sqlBlock
			}
		}
		SendSSEProgressDetail(w, flusher, "plan", "completed", planSummary)

		messages := make([]*model.AIChatMessage, 0, len(*responses))
		var retryReason string
		for _, response := range *responses {
			if unsupportedChatTool(response.Text) {
				retryReason = "This chat cannot invoke named tools. Answer using listed database tables or explain the limitation."
				break
			}
			if response.Type == types.ChatMessageTypeSQL {
				step := "query"
				if sqlguard.Classify(response.Text).Mutating {
					step = "draft"
				}
				SendSSEProgressDetail(w, flusher, step, "started", response.Text)
				message := processFinalResponse(ctx, &response, queryRunner)
				stepDetail := response.Text
				if message.Type == "error" {
					stepDetail = fmt.Sprintf("%s\n→ 执行失败：%s", response.Text, message.Text)
				} else if message.RequiresConfirmation {
					stepDetail = fmt.Sprintf("%s\n→ 已生成变更语句，等待人工确认", response.Text)
				} else if message.Result != nil {
					stepDetail = fmt.Sprintf("%s\n→ 查询成功，返回 %d 行数据", response.Text, len(message.Result.Rows))
				}
				SendSSEProgressDetail(w, flusher, step, "completed", stepDetail)
				if message.Type == "error" {
					retryReason = message.Text
					lastFailedSQL = response.Text
					lastQueryError = message.Text
					break
				}
				messages = append(messages, message)
				continue
			}
			message := processFinalResponse(ctx, &response, queryRunner)
			messages = append(messages, message)
		}
		if retryReason != "" && attempt == 0 {
			retryDetail := fmt.Sprintf("上一次查询失败：%s\n正在根据错误信息重新生成 SQL...", retryReason)
			SendSSEProgressDetail(w, flusher, "retry", "started", retryDetail)
			request = userQuery + "\nThe previous plan failed: " + retryReason + "\nReturn a corrected answer. Use only SQL on the listed database tables; do not claim that any action has run."
			SendSSEProgressDetail(w, flusher, "retry", "completed", retryDetail)
			continue
		}
		if retryReason != "" {
			switch {
			case lastFailedSQL != "":
				SendSSESQLFailure(w, flusher, lastFailedSQL, lastQueryError)
			case strings.Contains(retryReason, "cannot invoke named tools"):
				SendSSEMessage(w, flusher, &model.AIChatMessage{Type: "scope:error", Text: "unsupported_tool"})
				SendSSEDone(w, flusher)
			default:
				SendSSEError(w, flusher, retryReason)
			}
			return
		}
		hasSQL := false
		for _, response := range *responses {
			if response.Type == types.ChatMessageTypeSQL {
				hasSQL = true
				break
			}
		}
		if lastFailedSQL != "" && !hasSQL {
			SendSSESQLFailure(w, flusher, lastFailedSQL, lastQueryError)
			return
		}
		for _, message := range messages {
			SendSSEMessage(w, flusher, message)
		}
		SendSSEDone(w, flusher)
		return
	}
}

func sendModelError(w http.ResponseWriter, flusher http.Flusher, message string) {
	lower := strings.ToLower(message)
	if strings.Contains(lower, "connection refused") || strings.Contains(lower, "could not connect") || strings.Contains(lower, "connect error") {
		SendSSEMessage(w, flusher, &model.AIChatMessage{Type: "provider:error", Text: sanitizeErrorMessage(message)})
		SendSSEDone(w, flusher)
		return
	}
	SendSSEError(w, flusher, message)
}

func unsupportedChatTool(text string) bool {
	var value map[string]json.RawMessage
	return json.Unmarshal([]byte(strings.TrimSpace(text)), &value) == nil && value["toolName"] != nil
}

func processFinalResponse(ctx stdctx.Context, bamlResp *types.ChatResponse, queryRunner source.ReadOnlyQueryRunner) *model.AIChatMessage {
	message := &model.AIChatMessage{
		Type: bamlconfig.ConvertBAMLTypeToWhoDB(bamlResp.Type),
		Text: bamlResp.Text,
	}

	if bamlResp.Type != types.ChatMessageTypeSQL {
		return message
	}
	if bamlResp.Operation == nil {
		message.Type = "error"
		message.Text = "The model did not specify a query operation"
		return message
	}

	classification := sqlguard.Classify(bamlResp.Text)

	if classification.Mutating {
		message.Type = "sql:" + sqlguard.OperationName(classification)
		message.RequiresConfirmation = true
		return message
	}

	result, err := queryRunner.RunReadOnlyQuery(ctx, bamlResp.Text)
	if err != nil {
		message.Type = "error"
		message.Text = err.Error()
		return message
	}

	message.Type = "sql:get"
	message.Result = ConvertResultToMessage(result)
	return message
}
