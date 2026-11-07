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

import type { PayloadAction} from '@reduxjs/toolkit';
import {createSlice} from '@reduxjs/toolkit';
import type {SortCondition, WhereCondition} from '@graphql';

type ExploreConditionEntry = {
    whereCondition?: WhereCondition;
    sortConditions: SortCondition[];
};

/** Persisted history entry for SQL queries executed from the explore storage unit SQL console. */
export type ExploreSqlHistoryItem = {
    id: string;
    sql: string;
    table?: string;
    database?: string;
    status: boolean;
    rowCount?: number;
    timestamp: string;
};

type IExploreConditionsState = {
    conditions: Record<string, ExploreConditionEntry>;
    sqlHistory?: ExploreSqlHistoryItem[];
    lastSqlByKey?: Record<string, string>;
};

const MAX_SQL_HISTORY_ITEMS = 200;

const normalizeSqlForDedup = (sql: string): string =>
    sql.trim().replace(/;+\s*$/, '').replace(/\s+/g, ' ');

const initialState: IExploreConditionsState = {
    conditions: {},
    sqlHistory: [],
    lastSqlByKey: {},
};

export const exploreConditionsSlice = createSlice({
    name: 'exploreConditions',
    initialState,
    reducers: {
        setExploreConditions: (state, action: PayloadAction<{ key: string } & ExploreConditionEntry>) => {
            const { key, whereCondition, sortConditions } = action.payload;
            state.conditions[key] = { whereCondition, sortConditions };
        },
        setLastSql: (state, action: PayloadAction<{ key: string; sql: string }>) => {
            state.lastSqlByKey ??= {};
            state.lastSqlByKey[action.payload.key] = action.payload.sql;
        },
        addSqlHistory: (
            state,
            action: PayloadAction<{
                sql: string;
                table?: string;
                database?: string;
                status: boolean;
                rowCount?: number;
            }>
        ) => {
            const trimmedSql = action.payload.sql.trim();
            if (!trimmedSql) {
                return;
            }
            if (!Array.isArray(state.sqlHistory)) {
                state.sqlHistory = [];
            }
            // Deduplicate identical SQL for the same table and database so re-running moves it to the top
            const normalizedNew = normalizeSqlForDedup(trimmedSql);
            state.sqlHistory = state.sqlHistory.filter(
                item => !(
                    normalizeSqlForDedup(item.sql) === normalizedNew &&
                    (item.table ?? '') === (action.payload.table ?? '') &&
                    (item.database ?? '') === (action.payload.database ?? '')
                )
            );
            state.sqlHistory.unshift({
                id: `${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
                sql: trimmedSql,
                table: action.payload.table,
                database: action.payload.database,
                status: action.payload.status,
                rowCount: action.payload.rowCount,
                timestamp: new Date().toISOString(),
            });
            if (state.sqlHistory.length > MAX_SQL_HISTORY_ITEMS) {
                state.sqlHistory = state.sqlHistory.slice(0, MAX_SQL_HISTORY_ITEMS);
            }
        },
        removeSqlHistoryItem: (state, action: PayloadAction<{ id: string }>) => {
            if (!Array.isArray(state.sqlHistory)) {
                return;
            }
            const target = state.sqlHistory.find(item => item.id === action.payload.id);
            if (!target) {
                return;
            }
            const targetSql = normalizeSqlForDedup(target.sql);
            state.sqlHistory = state.sqlHistory.filter(
                item => !(
                    item.id === action.payload.id ||
                    (normalizeSqlForDedup(item.sql) === targetSql &&
                        (item.table ?? '') === (target.table ?? '') &&
                        (item.database ?? '') === (target.database ?? ''))
                )
            );
        },
        clearSqlHistory: (state, action: PayloadAction<{ table?: string; database?: string } | undefined>) => {
            if (!Array.isArray(state.sqlHistory)) {
                state.sqlHistory = [];
                return;
            }
            const targetTable = action.payload?.table;
            const targetDb = action.payload?.database;
            if (!targetTable) {
                state.sqlHistory = [];
                return;
            }
            state.sqlHistory = state.sqlHistory.filter(
                item => !(item.table === targetTable && (!targetDb || !item.database || item.database === targetDb))
            );
        },
    },
});

export const ExploreConditionsActions = exploreConditionsSlice.actions;
export const exploreConditionsReducers = exploreConditionsSlice.reducer;
