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

import type { RecordInput, SourceProfilesQuery } from '@graphql';
import type { SourceCredentialValue, SourceLoginPayload } from '../store/auth';

type RecordLike = { Key: string; Value: string };

export function valuesToMap(values: readonly RecordLike[] | undefined): Record<string, string> {
    return (values ?? []).reduce<Record<string, string>>((acc, value) => {
        acc[value.Key] = value.Value;
        return acc;
    }, {});
}

export function getValue(values: readonly RecordLike[] | undefined, key: string): string {
    return valuesToMap(values)[key] ?? "";
}

export function buildSourceValues(
    hostName: string,
    database: string,
    username: string,
    password: string,
    advancedForm: Record<string, string>,
): SourceCredentialValue[] {
    const values: SourceCredentialValue[] = [];

    if (hostName) {
        values.push({ Key: "Hostname", Value: hostName });
    }
    if (database) {
        values.push({ Key: "Database", Value: database });
    }
    if (username) {
        values.push({ Key: "Username", Value: username });
    }
    if (password) {
        values.push({ Key: "Password", Value: password });
    }

    Object.entries(advancedForm).forEach(([key, value]) => {
        if (value === "") {
            return;
        }
        values.push({ Key: key, Value: value });
    });

    return values;
}

export function buildRecordInputs(
    hostName: string,
    database: string,
    username: string,
    password: string,
    advancedForm: Record<string, string>,
): RecordInput[] {
    return buildSourceValues(hostName, database, username, password, advancedForm).map(value => ({
        Key: value.Key,
        Value: value.Value,
    }));
}

export function createProfilePayload(
    id: string | undefined,
    sourceType: string,
    values: SourceCredentialValue[],
    extras: Partial<SourceLoginPayload> = {},
): SourceLoginPayload {
    return {
        Id: id,
        SourceType: sourceType,
        Values: values,
        ...extras,
    };
}

export function createProfilePayloadFromSourceProfile(
    profile: SourceProfilesQuery['SourceProfiles'][number],
    databaseOverride?: string,
): SourceLoginPayload {
    const valuesMap = valuesToMap(profile.Values);
    if (databaseOverride) {
        valuesMap.Database = databaseOverride;
    }

    const values = Object.entries(valuesMap).map(([Key, Value]) => ({ Key, Value }));

    return {
        Id: profile.Id,
        SourceType: profile.Type,
        Values: values,
        Saved: true,
        DisplayName: profile.Alias,
        ProjectId: profile.ProjectId ?? undefined,
        ProjectName: profile.ProjectName ?? undefined,
        IsEnvironmentDefined: profile.IsEnvironmentDefined,
        SSLConfigured: profile.SSLConfigured,
        Source: profile.Source,
    };
}
