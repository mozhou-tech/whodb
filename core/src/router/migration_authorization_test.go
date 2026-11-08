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

package router

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/99designs/gqlgen/graphql"
	graphapi "github.com/clidey/whodb/core/graph"
	"github.com/clidey/whodb/core/src/auth"
)

func TestMigrationRecoveryRequiresAuthentication(t *testing.T) {
	t.Setenv("WHODB_DESKTOP", "false")
	t.Setenv("WHODB_CLI", "false")
	for _, query := range []string{
		`query Version { Version diagnosis: SourceMigrationDiagnosis(input:{Target:{SourceType:"MySQL"},Directory:"/tmp"}){Healthy} }`,
		`mutation Version { recovery: RepairSourceMigrations(input:{Migration:{Target:{SourceType:"MySQL"},Directory:"/tmp"},Token:"not-a-plan",ConfirmPartialChangesResolved:true}){Success} }`,
	} {
		called := false
		authorize := func(ctx context.Context, next graphql.OperationHandler) graphql.ResponseHandler {
			return auth.GraphQLAuthorizationMiddleware(ctx, func(ctx context.Context) graphql.ResponseHandler {
				called = true
				return next(ctx)
			})
		}
		server := httptest.NewServer(NewGraphQLServer(graphapi.NewExecutableSchema(graphapi.Config{Resolvers: &graphapi.Resolver{}}), authorize))
		body, err := json.Marshal(map[string]string{"query": query, "operationName": "Version"})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, server.URL, strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(req)
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		var result struct {
			Errors []struct {
				Extensions map[string]any `json:"extensions"`
			} `json:"errors"`
		}
		err = json.NewDecoder(response.Body).Decode(&result)
		_ = response.Body.Close()
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		if called || len(result.Errors) == 0 || result.Errors[0].Extensions["code"] != "UNAUTHENTICATED" {
			t.Fatalf("protected recovery field reached resolver: called=%v response=%+v", called, result)
		}
	}
}
