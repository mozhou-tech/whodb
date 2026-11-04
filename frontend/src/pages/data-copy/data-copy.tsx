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

import { skipToken, useLazyQuery, useMutation, useQuery } from "@apollo/client/react";
import {
    Badge,
    Button,
    Checkbox,
    cn,
    Input,
    Label,
    SearchInput,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
    Table,
    TableBody,
    TableCell,
    TableHead,
    TableHeader,
    TableHeadRow,
    TableRow,
    toast,
    IconRefresh as ArrowPathIcon,
    IconCheckCircle as CheckCircleIcon,
    IconCopy as DocumentDuplicateIcon,
    IconInfo as InformationCircleIcon,
    IconTable as TableCellsIcon,
} from "@clidey/ux";
import {
    CopyDataMode,
    CopyExistingStrategy,
    CopySourceObjectsDocument,
    type CopySourceObjectsMutation,
    CopyTableStatus,
    GetStorageUnitsDocument,
    type GetStorageUnitsQuery,
    SourceProfilesDocument,
    type SourceProfilesQuery,
    SourceTargetDatabasesDocument,
    SourceTargetSchemasDocument,
    type SourceTargetConnectionInput,
} from "@graphql";
import { type FC, useCallback, useEffect, useMemo, useState } from "react";
import { InternalPage } from "../../components/page";
import { Loading } from "../../components/loading";
import { InternalRoutes } from "../../config/routes";
import { useTranslation } from "../../hooks/use-translation";
import { useSourceContract } from "../../hooks/useSourceContract";
import { useAppSelector } from "../../store/hooks";
import type { LocalLoginProfile } from "../../store/auth";
import { buildSourceParentRef } from "../../utils/source-refs";

type SourceBrowserObject = GetStorageUnitsQuery["StorageUnit"][number];
type RemoteSourceProfile = SourceProfilesQuery["SourceProfiles"][number];
type CopyResultData = CopySourceObjectsMutation["CopySourceObjects"];

const LOCAL_PROJECT_KEY = "__local__";

interface TargetInstanceOption {
    id: string;
    displayName: string;
    projectKey: string;
    projectLabel: string;
    environmentLabel?: string;
    sourceType: string;
    defaultDatabase: string;
    isRemoteProfile: boolean;
    localProfile?: LocalLoginProfile;
}

function getRecordValue(
    values: ReadonlyArray<{ Key: string; Value: string }> | undefined,
    keys: string[]
): string | undefined {
    if (!values) {
        return undefined;
    }
    const lowerKeys = keys.map(k => k.toLowerCase());
    return values.find(entry => lowerKeys.includes(entry.Key.toLowerCase()))?.Value;
}

function formatLocalProfileName(profile: LocalLoginProfile): string {
    if (profile.DisplayName) {
        return profile.Hostname && !profile.DisplayName.includes(profile.Hostname)
            ? `${profile.DisplayName} (${profile.Hostname})`
            : profile.DisplayName;
    }
    if (profile.Hostname && profile.Username) {
        return `${profile.Username}@${profile.Hostname}`;
    }
    return profile.Hostname || profile.Database || profile.Id;
}

/**
 * Page component that allows copying selected tables/storage units (structure and/or data)
 * from the currently active project and database to another project and database.
 */
export const DataCopyPage: FC = () => {
    const { t } = useTranslation("pages/data-copy");
    const current = useAppSelector(state => state.auth.current);
    const localProfiles = useAppSelector(state => state.auth.profiles);
    const reduxSchema = useAppSelector(state => state.database.schema);

    const currentType = current?.Type;
    const currentDatabase = current?.Database ?? "";
    const {
        item,
        storageUnitLabel,
        supportsDatabaseSwitching,
        supportsSchema,
        usesDatabaseInsteadOfSchema,
    } = useSourceContract(currentType);

    const effectiveSourceSchema = usesDatabaseInsteadOfSchema ? currentDatabase : reduxSchema;

    const parentRef = useMemo(
        () => buildSourceParentRef(item, current, effectiveSourceSchema),
        [current, effectiveSourceSchema, item]
    );

    const shouldSkipSourceObjects = usesDatabaseInsteadOfSchema && !currentDatabase;
    const {
        data: storageUnitsData,
        loading: storageUnitsLoading,
        refetch: refetchStorageUnits,
    } = useQuery(
        GetStorageUnitsDocument,
        shouldSkipSourceObjects
            ? skipToken
            : {
                variables: {
                    parent: parentRef,
                },
            }
    );

    const { data: profilesData } = useQuery(SourceProfilesDocument, {
        fetchPolicy: "cache-and-network",
    });

    const [fetchTargetDatabases, { loading: targetDatabasesLoading }] = useLazyQuery(
        SourceTargetDatabasesDocument,
        { fetchPolicy: "network-only" }
    );
    const [fetchTargetSchemas, { loading: targetSchemasLoading }] = useLazyQuery(
        SourceTargetSchemasDocument,
        { fetchPolicy: "network-only" }
    );
    const [executeCopy, { loading: copying }] = useMutation(CopySourceObjectsDocument);

    const storageUnits = useMemo<SourceBrowserObject[]>(() => {
        const raw = (storageUnitsData?.StorageUnit ?? []) as SourceBrowserObject[];
        return [...raw].sort((a, b) => (a.Name ?? "").localeCompare(b.Name ?? ""));
    }, [storageUnitsData?.StorageUnit]);

    // Build unified list of compatible target instances (same database type) grouped by project.
    const compatibleInstances = useMemo<TargetInstanceOption[]>(() => {
        if (!currentType) {
            return [];
        }
        const byId = new Map<string, TargetInstanceOption>();

        const remoteProfiles: RemoteSourceProfile[] = profilesData?.SourceProfiles ?? [];
        for (const profile of remoteProfiles) {
            if (profile.Type !== currentType) {
                continue;
            }
            const projectName = profile.ProjectName || getRecordValue(profile.Values, ["ProjectName", "ProjectCode"]);
            const envName = getRecordValue(profile.Values, ["EnvironmentName", "EnvironmentCode"]);
            const dbName = getRecordValue(profile.Values, ["Database"]) ?? "";
            const hostName = getRecordValue(profile.Values, ["Hostname"]) ?? "";
            const rawAlias = profile.Alias || profile.Id;
            const displayName = hostName && !rawAlias.includes(hostName)
                ? `${rawAlias} (${hostName})`
                : rawAlias;

            byId.set(profile.Id, {
                id: profile.Id,
                displayName,
                projectKey: projectName || LOCAL_PROJECT_KEY,
                projectLabel: projectName || t("localConnectionsGroup"),
                environmentLabel: envName,
                sourceType: profile.Type,
                defaultDatabase: dbName,
                isRemoteProfile: true,
            });
        }

        for (const local of localProfiles) {
            if (local.Type !== currentType || byId.has(local.Id)) {
                continue;
            }
            const projectName = local.ProjectName || getRecordValue(local.Advanced, ["ProjectName", "ProjectCode"]);
            const envName = getRecordValue(local.Advanced, ["EnvironmentName", "EnvironmentCode"]);
            byId.set(local.Id, {
                id: local.Id,
                displayName: formatLocalProfileName(local),
                projectKey: projectName || LOCAL_PROJECT_KEY,
                projectLabel: projectName || t("localConnectionsGroup"),
                environmentLabel: envName,
                sourceType: local.Type,
                defaultDatabase: local.Database ?? "",
                isRemoteProfile: Boolean(local.Saved),
                localProfile: local,
            });
        }

        if (current && current.Type === currentType && !byId.has(current.Id)) {
            const projectName = current.ProjectName || getRecordValue(current.Advanced, ["ProjectName", "ProjectCode"]);
            const envName = getRecordValue(current.Advanced, ["EnvironmentName", "EnvironmentCode"]);
            byId.set(current.Id, {
                id: current.Id,
                displayName: formatLocalProfileName(current),
                projectKey: projectName || LOCAL_PROJECT_KEY,
                projectLabel: projectName || t("localConnectionsGroup"),
                environmentLabel: envName,
                sourceType: current.Type,
                defaultDatabase: current.Database ?? "",
                isRemoteProfile: Boolean(current.Saved),
                localProfile: current,
            });
        }

        return Array.from(byId.values());
    }, [current, currentType, localProfiles, profilesData?.SourceProfiles, t]);

    const projectOptions = useMemo(() => {
        const map = new Map<string, string>();
        for (const inst of compatibleInstances) {
            if (!map.has(inst.projectKey)) {
                map.set(inst.projectKey, inst.projectLabel);
            }
        }
        return Array.from(map.entries()).map(([key, label]) => ({ key, label }));
    }, [compatibleInstances]);

    const currentInstanceMeta = useMemo(
        () => compatibleInstances.find(inst => inst.id === current?.Id),
        [compatibleInstances, current?.Id]
    );

    const [selectedProjectKey, setSelectedProjectKey] = useState<string>("");
    const [selectedInstanceId, setSelectedInstanceId] = useState<string>("");
    const [targetDatabases, setTargetDatabases] = useState<string[]>([]);
    const [selectedTargetDatabase, setSelectedTargetDatabase] = useState<string>("");
    const [targetSchemas, setTargetSchemas] = useState<string[]>([]);
    const [selectedTargetSchema, setSelectedTargetSchema] = useState<string>("");

    const [copyMode, setCopyMode] = useState<CopyDataMode>(CopyDataMode.StructureAndData);
    const [existingStrategy, setExistingStrategy] = useState<CopyExistingStrategy>(CopyExistingStrategy.Skip);
    const [filterValue, setFilterValue] = useState<string>("");
    const [selectedNames, setSelectedNames] = useState<Set<string>>(new Set());
    const [copyResult, setCopyResult] = useState<CopyResultData | null>(null);

    // Initialize default target project when options load.
    useEffect(() => {
        if (projectOptions.length === 0) {
            return;
        }
        if (!selectedProjectKey || !projectOptions.some(p => p.key === selectedProjectKey)) {
            const preferredProject = currentInstanceMeta?.projectKey ?? projectOptions[0].key;
            setSelectedProjectKey(preferredProject);
        }
    }, [currentInstanceMeta?.projectKey, projectOptions, selectedProjectKey]);

    const instancesInProject = useMemo(
        () => compatibleInstances.filter(inst => inst.projectKey === selectedProjectKey),
        [compatibleInstances, selectedProjectKey]
    );

    // Initialize default target instance when project changes.
    useEffect(() => {
        if (instancesInProject.length === 0) {
            setSelectedInstanceId("");
            return;
        }
        if (!selectedInstanceId || !instancesInProject.some(inst => inst.id === selectedInstanceId)) {
            const preferred = instancesInProject.find(inst => inst.id === current?.Id) ?? instancesInProject[0];
            setSelectedInstanceId(preferred.id);
        }
    }, [current?.Id, instancesInProject, selectedInstanceId]);

    const selectedTargetInstance = useMemo(
        () => compatibleInstances.find(inst => inst.id === selectedInstanceId),
        [compatibleInstances, selectedInstanceId]
    );

    const buildTargetConnectionInput = useCallback(
        (databaseOverride?: string): SourceTargetConnectionInput | null => {
            if (!selectedTargetInstance) {
                return null;
            }
            const dbName = databaseOverride !== undefined
                ? databaseOverride
                : (selectedTargetDatabase || selectedTargetInstance.defaultDatabase);

            if (selectedTargetInstance.isRemoteProfile) {
                return {
                    ProfileId: selectedTargetInstance.id,
                    SourceType: selectedTargetInstance.sourceType,
                    Database: dbName || undefined,
                };
            }

            const local = selectedTargetInstance.localProfile;
            const values = (local?.Values ?? []).map(entry => ({
                Key: entry.Key,
                Value: entry.Key === "Database" && dbName ? dbName : entry.Value,
            }));
            if (dbName && !values.some(entry => entry.Key === "Database")) {
                values.push({ Key: "Database", Value: dbName });
            }

            return {
                ProfileId: selectedTargetInstance.id,
                SourceType: selectedTargetInstance.sourceType,
                Database: dbName || undefined,
                Values: values,
            };
        },
        [selectedTargetDatabase, selectedTargetInstance]
    );

    // Load available databases on the selected target instance.
    useEffect(() => {
        if (!selectedTargetInstance) {
            setTargetDatabases([]);
            setSelectedTargetDatabase("");
            return;
        }
        const targetInput = buildTargetConnectionInput(selectedTargetInstance.defaultDatabase);
        if (!targetInput) {
            return;
        }
        let cancelled = false;
        fetchTargetDatabases({ variables: { target: targetInput } })
            .then(res => {
                if (cancelled) {
                    return;
                }
                const dbs = res.data?.SourceTargetDatabases ?? [];
                setTargetDatabases(dbs);
                setSelectedTargetDatabase(prev => {
                    if (prev && dbs.includes(prev)) {
                        return prev;
                    }
                    // Prefer a different database than source if target is the same instance
                    if (selectedTargetInstance.id === current?.Id && dbs.length > 1) {
                        const different = dbs.find(db => db !== currentDatabase);
                        if (different) {
                            return different;
                        }
                    }
                    if (selectedTargetInstance.defaultDatabase && dbs.includes(selectedTargetInstance.defaultDatabase)) {
                        return selectedTargetInstance.defaultDatabase;
                    }
                    return dbs[0] ?? selectedTargetInstance.defaultDatabase ?? "";
                });
            })
            .catch(() => {
                if (!cancelled) {
                    setTargetDatabases([]);
                    setSelectedTargetDatabase(selectedTargetInstance.defaultDatabase ?? "");
                }
            });
        return () => {
            cancelled = true;
        };
    }, [
        // Only re-fetch databases when the target instance changes
        // eslint-disable-next-line react-hooks/exhaustive-deps
        selectedTargetInstance?.id,
    ]);

    const showSchemaSelector = supportsSchema && !usesDatabaseInsteadOfSchema;

    // Load available schemas on the selected target instance + target database.
    useEffect(() => {
        if (!showSchemaSelector || !selectedTargetInstance || !selectedTargetDatabase) {
            setTargetSchemas([]);
            setSelectedTargetSchema("");
            return;
        }
        const targetInput = buildTargetConnectionInput(selectedTargetDatabase);
        if (!targetInput) {
            return;
        }
        let cancelled = false;
        fetchTargetSchemas({ variables: { target: targetInput } })
            .then(res => {
                if (cancelled) {
                    return;
                }
                const schemas = res.data?.SourceTargetSchemas ?? [];
                setTargetSchemas(schemas);
                setSelectedTargetSchema(prev => {
                    if (prev && schemas.includes(prev)) {
                        return prev;
                    }
                    if (reduxSchema && schemas.includes(reduxSchema)) {
                        return reduxSchema;
                    }
                    return schemas[0] ?? reduxSchema ?? "";
                });
            })
            .catch(() => {
                if (!cancelled) {
                    setTargetSchemas([]);
                    setSelectedTargetSchema(reduxSchema ?? "");
                }
            });
        return () => {
            cancelled = true;
        };
    }, [
        showSchemaSelector,
        selectedTargetInstance?.id,
        selectedTargetDatabase,
        reduxSchema,
        // eslint-disable-next-line react-hooks/exhaustive-deps
        fetchTargetSchemas,
    ]);

    // Clear selected table names when switching source context.
    useEffect(() => {
        setSelectedNames(new Set());
        setCopyResult(null);
    }, [current?.Id, currentDatabase, effectiveSourceSchema]);

    const filteredUnits = useMemo(() => {
        const q = filterValue.trim().toLowerCase();
        if (!q) {
            return storageUnits;
        }
        return storageUnits.filter(u => (u.Name ?? "").toLowerCase().includes(q));
    }, [filterValue, storageUnits]);

    const handleToggleTable = useCallback((name: string) => {
        setSelectedNames(prev => {
            const next = new Set(prev);
            if (next.has(name)) {
                next.delete(name);
            } else {
                next.add(name);
            }
            return next;
        });
    }, []);

    const handleSelectAllFiltered = useCallback(() => {
        setSelectedNames(prev => {
            const next = new Set(prev);
            for (const unit of filteredUnits) {
                if (unit.Name) {
                    next.add(unit.Name);
                }
            }
            return next;
        });
    }, [filteredUnits]);

    const handleInvertFiltered = useCallback(() => {
        setSelectedNames(prev => {
            const next = new Set(prev);
            for (const unit of filteredUnits) {
                if (!unit.Name) {
                    continue;
                }
                if (next.has(unit.Name)) {
                    next.delete(unit.Name);
                } else {
                    next.add(unit.Name);
                }
            }
            return next;
        });
    }, [filteredUnits]);

    const handleClearSelection = useCallback(() => {
        setSelectedNames(new Set());
    }, []);

    const isSameSourceAndTarget = useMemo(() => {
        if (!selectedTargetInstance || !current) {
            return false;
        }
        if (selectedTargetInstance.id !== current.Id) {
            return false;
        }
        const sameDB = (selectedTargetDatabase || "") === (currentDatabase || "");
        if (!sameDB) {
            return false;
        }
        if (showSchemaSelector) {
            return (selectedTargetSchema || "") === (reduxSchema || "");
        }
        return true;
    }, [
        current,
        currentDatabase,
        reduxSchema,
        selectedTargetDatabase,
        selectedTargetInstance,
        selectedTargetSchema,
        showSchemaSelector,
    ]);

    const handleStartCopy = useCallback(async () => {
        if (selectedNames.size === 0) {
            toast.error(t("noTablesSelectedError", { storageUnit: storageUnitLabel }));
            return;
        }
        if (!selectedTargetInstance || (supportsDatabaseSwitching && !selectedTargetDatabase)) {
            toast.error(t("targetIncompleteError"));
            return;
        }
        if (isSameSourceAndTarget) {
            toast.error(t("sameSourceAndTargetError"));
            return;
        }

        const targetInput = buildTargetConnectionInput(selectedTargetDatabase);
        if (!targetInput) {
            toast.error(t("targetIncompleteError"));
            return;
        }

        const targetSchemaParam = usesDatabaseInsteadOfSchema
            ? selectedTargetDatabase
            : (showSchemaSelector ? selectedTargetSchema : "");

        try {
            const response = await executeCopy({
                variables: {
                    input: {
                        SourceParent: parentRef,
                        Target: targetInput,
                        TargetSchema: targetSchemaParam || undefined,
                        ObjectNames: Array.from(selectedNames),
                        Mode: copyMode,
                        ExistingStrategy: existingStrategy,
                    },
                },
            });

            const result = response.data?.CopySourceObjects;
            if (!result) {
                return;
            }
            setCopyResult(result);

            if (result.FailedCount === 0 && result.SkippedCount === 0) {
                toast.success(
                    t("copySuccessToast", {
                        succeeded: result.SucceededCount,
                        total: result.TotalTables,
                        storageUnit: storageUnitLabel,
                        rows: result.TotalRowsCopied,
                    })
                );
            } else {
                toast.info(
                    t("copyPartialToast", {
                        succeeded: result.SucceededCount,
                        skipped: result.SkippedCount,
                        failed: result.FailedCount,
                    })
                );
            }
        } catch (err) {
            const errMsg = err instanceof Error ? err.message : String(err);
            toast.error(t("copyFailedToast", { error: errMsg }));
        }
    }, [
        buildTargetConnectionInput,
        copyMode,
        executeCopy,
        existingStrategy,
        isSameSourceAndTarget,
        parentRef,
        selectedNames,
        selectedTargetDatabase,
        selectedTargetInstance,
        selectedTargetSchema,
        showSchemaSelector,
        storageUnitLabel,
        supportsDatabaseSwitching,
        t,
        usesDatabaseInsteadOfSchema,
    ]);

    const routes = useMemo(
        () => [
            {
                ...InternalRoutes.DataCopy,
                name: t("title"),
            },
        ],
        [t]
    );

    const allFilteredSelected = useMemo(() => {
        if (filteredUnits.length === 0) {
            return false;
        }
        return filteredUnits.every(u => u.Name && selectedNames.has(u.Name));
    }, [filteredUnits, selectedNames]);

    return (
        <InternalPage routes={routes}>
            <div className="flex flex-col gap-6 w-full max-w-6xl mx-auto pb-8" data-testid="data-copy-page">
                {/* Page Header */}
                <div className="flex items-start justify-between gap-4 flex-wrap">
                    <div className="flex flex-col gap-1">
                        <div className="flex items-center gap-2">
                            <DocumentDuplicateIcon className="w-5 h-5 text-brand" />
                            <h1 className="text-xl font-semibold tracking-tight">{t("title")}</h1>
                        </div>
                        <p className="text-sm text-muted-foreground">
                            {t("subtitle", { storageUnit: storageUnitLabel })}
                        </p>
                    </div>
                    <Button
                        variant="outline"
                        size="sm"
                        onClick={() => void refetchStorageUnits()}
                        disabled={storageUnitsLoading}
                        data-testid="data-copy-refresh-button"
                    >
                        <ArrowPathIcon className="w-4 h-4 mr-1.5" />
                        {t("refreshSourceButton")}
                    </Button>
                </div>

                {/* Source & Target Cards */}
                <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
                    {/* Source Summary */}
                    <div className="rounded-xl border border-border bg-card p-5 flex flex-col gap-4 shadow-xs">
                        <div className="flex items-center justify-between">
                            <span className="text-sm font-semibold">{t("sourceSectionTitle")}</span>
                            {currentType && <Badge variant="secondary">{currentType}</Badge>}
                        </div>
                        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3 text-sm">
                            <div className="flex flex-col gap-1 rounded-lg bg-muted/40 p-3">
                                <span className="text-xs text-muted-foreground">{t("projectLabel")}</span>
                                <span className="font-medium truncate">
                                    {currentInstanceMeta?.projectLabel || t("localConnectionsGroup")}
                                </span>
                            </div>
                            <div className="flex flex-col gap-1 rounded-lg bg-muted/40 p-3">
                                <span className="text-xs text-muted-foreground">{t("instanceLabel")}</span>
                                <span className="font-medium truncate">
                                    {currentInstanceMeta?.displayName || (current ? formatLocalProfileName(current) : "-")}
                                </span>
                            </div>
                            {supportsDatabaseSwitching && (
                                <div className="flex flex-col gap-1 rounded-lg bg-muted/40 p-3">
                                    <span className="text-xs text-muted-foreground">{t("databaseLabel")}</span>
                                    <span className="font-medium truncate">{currentDatabase || "-"}</span>
                                </div>
                            )}
                            {showSchemaSelector && (
                                <div className="flex flex-col gap-1 rounded-lg bg-muted/40 p-3">
                                    <span className="text-xs text-muted-foreground">{t("schemaLabel")}</span>
                                    <span className="font-medium truncate">{reduxSchema || "-"}</span>
                                </div>
                            )}
                        </div>
                    </div>

                    {/* Target Selector */}
                    <div className="rounded-xl border border-border bg-card p-5 flex flex-col gap-4 shadow-xs">
                        <div className="flex items-center justify-between">
                            <span className="text-sm font-semibold">{t("targetSectionTitle")}</span>
                            {currentType && <Badge variant="outline">{currentType}</Badge>}
                        </div>
                        <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
                            {/* Target Project */}
                            <div className="flex flex-col gap-1.5">
                                <Label className="text-xs text-muted-foreground">{t("projectLabel")}</Label>
                                <Select
                                    value={selectedProjectKey}
                                    onValueChange={val => {
                                        setSelectedProjectKey(val);
                                        setCopyResult(null);
                                    }}
                                >
                                    <SelectTrigger className="w-full" data-testid="target-project-select">
                                        <SelectValue placeholder={t("selectTargetProject")} />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {projectOptions.map(proj => (
                                            <SelectItem key={proj.key} value={proj.key}>
                                                {proj.label}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>

                            {/* Target Instance */}
                            <div className="flex flex-col gap-1.5">
                                <Label className="text-xs text-muted-foreground">{t("instanceLabel")}</Label>
                                <Select
                                    value={selectedInstanceId}
                                    onValueChange={val => {
                                        setSelectedInstanceId(val);
                                        setCopyResult(null);
                                    }}
                                    disabled={instancesInProject.length === 0}
                                >
                                    <SelectTrigger className="w-full" data-testid="target-instance-select">
                                        <SelectValue placeholder={t("selectTargetInstance")} />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {instancesInProject.map(inst => (
                                            <SelectItem key={inst.id} value={inst.id}>
                                                {inst.environmentLabel
                                                    ? `[${inst.environmentLabel}] ${inst.displayName}`
                                                    : inst.displayName}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>

                            {/* Target Database */}
                            {supportsDatabaseSwitching && (
                                <div className="flex flex-col gap-1.5">
                                    <Label className="text-xs text-muted-foreground">{t("databaseLabel")}</Label>
                                    {targetDatabases.length > 0 ? (
                                        <Select
                                            value={selectedTargetDatabase}
                                            onValueChange={val => {
                                                setSelectedTargetDatabase(val);
                                                setCopyResult(null);
                                            }}
                                            disabled={targetDatabasesLoading}
                                        >
                                            <SelectTrigger className="w-full" data-testid="target-database-select">
                                                <SelectValue placeholder={t("selectTargetDatabase")} />
                                            </SelectTrigger>
                                            <SelectContent>
                                                {targetDatabases.map(db => (
                                                    <SelectItem key={db} value={db}>
                                                        {db}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                    ) : (
                                        <Input
                                            value={selectedTargetDatabase}
                                            onChange={e => {
                                                setSelectedTargetDatabase(e.target.value);
                                                setCopyResult(null);
                                            }}
                                            placeholder={t("enterTargetDatabase")}
                                            data-testid="target-database-input"
                                        />
                                    )}
                                </div>
                            )}

                            {/* Target Schema */}
                            {showSchemaSelector && (
                                <div className="flex flex-col gap-1.5">
                                    <Label className="text-xs text-muted-foreground">{t("schemaLabel")}</Label>
                                    {targetSchemas.length > 0 ? (
                                        <Select
                                            value={selectedTargetSchema}
                                            onValueChange={val => {
                                                setSelectedTargetSchema(val);
                                                setCopyResult(null);
                                            }}
                                            disabled={targetSchemasLoading}
                                        >
                                            <SelectTrigger className="w-full" data-testid="target-schema-select">
                                                <SelectValue placeholder={t("selectTargetSchema")} />
                                            </SelectTrigger>
                                            <SelectContent>
                                                {targetSchemas.map(s => (
                                                    <SelectItem key={s} value={s}>
                                                        {s}
                                                    </SelectItem>
                                                ))}
                                            </SelectContent>
                                        </Select>
                                    ) : (
                                        <Input
                                            value={selectedTargetSchema}
                                            onChange={e => {
                                                setSelectedTargetSchema(e.target.value);
                                                setCopyResult(null);
                                            }}
                                            placeholder={t("enterTargetSchema")}
                                            data-testid="target-schema-input"
                                        />
                                    )}
                                </div>
                            )}
                        </div>
                        {isSameSourceAndTarget && (
                            <div className="flex items-center gap-2 text-xs text-amber-500 bg-amber-500/10 rounded-md px-3 py-2">
                                <InformationCircleIcon className="w-4 h-4 shrink-0" />
                                <span>{t("sameSourceAndTargetError")}</span>
                            </div>
                        )}
                    </div>
                </div>

                {/* Copy Mode & Conflict Strategy Options */}
                <div className="rounded-xl border border-border bg-card p-5 flex flex-col gap-4 shadow-xs">
                    <span className="text-sm font-semibold">{t("optionsSectionTitle")}</span>
                    <div className="grid grid-cols-1 lg:grid-cols-2 gap-5">
                        {/* Copy Mode */}
                        <div className="flex flex-col gap-2">
                            <Label className="text-xs text-muted-foreground">{t("copyModeLabel")}</Label>
                            <div className="grid grid-cols-1 sm:grid-cols-2 gap-2.5">
                                {(
                                    [
                                        {
                                            value: CopyDataMode.StructureAndData,
                                            title: t("modeStructureAndData"),
                                            desc: t("modeStructureAndDataDesc", { storageUnit: storageUnitLabel }),
                                        },
                                        {
                                            value: CopyDataMode.StructureOnly,
                                            title: t("modeStructureOnly"),
                                            desc: t("modeStructureOnlyDesc", { storageUnit: storageUnitLabel }),
                                        },
                                    ] as const
                                ).map(option => {
                                    const active = copyMode === option.value;
                                    return (
                                        <button
                                            key={option.value}
                                            type="button"
                                            onClick={() => setCopyMode(option.value)}
                                            className={cn(
                                                "flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition-colors cursor-pointer",
                                                active
                                                    ? "border-brand bg-brand/10"
                                                    : "border-border hover:bg-muted/50"
                                            )}
                                            data-testid={`copy-mode-${option.value}`}
                                        >
                                            <span className="text-sm font-medium">{option.title}</span>
                                            <span className="text-xs text-muted-foreground">{option.desc}</span>
                                        </button>
                                    );
                                })}
                            </div>
                        </div>

                        {/* Conflict Strategy */}
                        <div className="flex flex-col gap-2">
                            <Label className="text-xs text-muted-foreground">{t("existingStrategyLabel")}</Label>
                            <div className="grid grid-cols-1 sm:grid-cols-3 gap-2.5">
                                {(
                                    [
                                        {
                                            value: CopyExistingStrategy.Skip,
                                            title: t("strategySkip"),
                                            desc: t("strategySkipDesc", { storageUnit: storageUnitLabel }),
                                        },
                                        {
                                            value: CopyExistingStrategy.Overwrite,
                                            title: t("strategyOverwrite"),
                                            desc: t("strategyOverwriteDesc", { storageUnit: storageUnitLabel }),
                                        },
                                        {
                                            value: CopyExistingStrategy.Append,
                                            title: t("strategyAppend"),
                                            desc: t("strategyAppendDesc", { storageUnit: storageUnitLabel }),
                                        },
                                    ] as const
                                ).map(option => {
                                    const active = existingStrategy === option.value;
                                    return (
                                        <button
                                            key={option.value}
                                            type="button"
                                            onClick={() => setExistingStrategy(option.value)}
                                            className={cn(
                                                "flex flex-col items-start gap-1 rounded-lg border p-3 text-left transition-colors cursor-pointer",
                                                active
                                                    ? "border-brand bg-brand/10"
                                                    : "border-border hover:bg-muted/50"
                                            )}
                                            data-testid={`copy-strategy-${option.value}`}
                                        >
                                            <span className="text-sm font-medium">{option.title}</span>
                                            <span className="text-xs text-muted-foreground">{option.desc}</span>
                                        </button>
                                    );
                                })}
                            </div>
                        </div>
                    </div>
                </div>

                {/* Table Multi-Selection Card */}
                <div className="rounded-xl border border-border bg-card p-5 flex flex-col gap-4 shadow-xs">
                    <div className="flex items-center justify-between gap-3 flex-wrap">
                        <div className="flex items-center gap-2.5">
                            <span className="text-sm font-semibold">
                                {t("tablesSectionTitle", { storageUnit: storageUnitLabel })}
                            </span>
                            <Badge variant="secondary" data-testid="selected-tables-count">
                                {t("selectedCount", {
                                    selected: selectedNames.size,
                                    total: storageUnits.length,
                                })}
                            </Badge>
                        </div>

                        <div className="flex items-center gap-2 flex-wrap">
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={handleSelectAllFiltered}
                                disabled={filteredUnits.length === 0 || allFilteredSelected}
                                data-testid="select-all-tables-button"
                            >
                                {t("selectAll")}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={handleInvertFiltered}
                                disabled={filteredUnits.length === 0}
                                data-testid="invert-selection-button"
                            >
                                {t("invertSelection")}
                            </Button>
                            <Button
                                variant="ghost"
                                size="sm"
                                onClick={handleClearSelection}
                                disabled={selectedNames.size === 0}
                                data-testid="clear-selection-button"
                            >
                                {t("clearSelection")}
                            </Button>
                            <Button
                                variant="default"
                                size="sm"
                                onClick={() => void handleStartCopy()}
                                disabled={copying || selectedNames.size === 0 || isSameSourceAndTarget}
                                data-testid="execute-copy-button"
                            >
                                <DocumentDuplicateIcon className="w-4 h-4 mr-1.5" />
                                {copying
                                    ? t("copyingButton")
                                    : t("startCopyButton", { count: selectedNames.size })}
                            </Button>
                        </div>
                    </div>

                    <div className="w-full max-w-md">
                        <SearchInput
                            value={filterValue}
                            onChange={e => setFilterValue(e.target.value)}
                            placeholder={t("searchPlaceholder", { storageUnit: storageUnitLabel })}
                            data-testid="data-copy-search-input"
                        />
                    </div>

                    {storageUnitsLoading ? (
                        <div className="py-12 flex justify-center">
                            <Loading />
                        </div>
                    ) : storageUnits.length === 0 ? (
                        <div className="py-10 text-center text-sm text-muted-foreground">
                            {t("noTablesAvailable", { storageUnit: storageUnitLabel })}
                        </div>
                    ) : filteredUnits.length === 0 ? (
                        <div className="py-10 text-center text-sm text-muted-foreground">
                            {t("noSearchMatch", {
                                filter: filterValue,
                                storageUnit: storageUnitLabel,
                            })}
                        </div>
                    ) : (
                        <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-2.5 max-h-[420px] overflow-y-auto pr-1">
                            {filteredUnits.map(unit => {
                                const checked = selectedNames.has(unit.Name);
                                const typeAttr = unit.Attributes?.find(a => a.Key === "Type")?.Value;
                                const countAttr = unit.Attributes?.find(
                                    a => a.Key === "Count" || a.Key === "Total Size"
                                )?.Value;
                                return (
                                    <div
                                        key={unit.Name}
                                        role="checkbox"
                                        aria-checked={checked}
                                        tabIndex={0}
                                        onClick={() => handleToggleTable(unit.Name)}
                                        onKeyDown={e => {
                                            if (e.key === " " || e.key === "Enter") {
                                                e.preventDefault();
                                                handleToggleTable(unit.Name);
                                            }
                                        }}
                                        className={cn(
                                            "flex items-center justify-between gap-2 rounded-lg border px-3 py-2.5 cursor-pointer transition-colors select-none",
                                            checked
                                                ? "border-brand bg-brand/10"
                                                : "border-border hover:bg-muted/40"
                                        )}
                                        data-testid={`copy-table-item-${unit.Name}`}
                                    >
                                        <div className="flex items-center gap-2.5 min-w-0">
                                            <Checkbox
                                                checked={checked}
                                                onCheckedChange={() => handleToggleTable(unit.Name)}
                                                onClick={e => e.stopPropagation()}
                                            />
                                            <TableCellsIcon className="w-4 h-4 text-muted-foreground shrink-0" />
                                            <span className="text-sm font-medium truncate" title={unit.Name}>
                                                {unit.Name}
                                            </span>
                                        </div>
                                        <div className="flex items-center gap-1.5 shrink-0">
                                            {countAttr && (
                                                <span className="text-xs text-muted-foreground">{countAttr}</span>
                                            )}
                                            {typeAttr && (
                                                <Badge variant="outline" className="text-[10px] px-1.5 py-0">
                                                    {typeAttr}
                                                </Badge>
                                            )}
                                        </div>
                                    </div>
                                );
                            })}
                        </div>
                    )}
                </div>

                {/* Execution Results Card */}
                {copyResult && (
                    <div
                        className="rounded-xl border border-border bg-card p-5 flex flex-col gap-4 shadow-xs"
                        data-testid="data-copy-results"
                    >
                        <div className="flex items-center justify-between gap-3 flex-wrap">
                            <div className="flex items-center gap-2">
                                <CheckCircleIcon className="w-5 h-5 text-emerald-500" />
                                <span className="text-sm font-semibold">{t("resultsSectionTitle")}</span>
                            </div>
                            <div className="flex items-center gap-2 flex-wrap text-xs">
                                <Badge variant="secondary">
                                    {t("summaryTotal")}: {copyResult.TotalTables}
                                </Badge>
                                <Badge variant="default">
                                    {t("summarySucceeded")}: {copyResult.SucceededCount}
                                </Badge>
                                {copyResult.SkippedCount > 0 && (
                                    <Badge variant="outline">
                                        {t("summarySkipped")}: {copyResult.SkippedCount}
                                    </Badge>
                                )}
                                {copyResult.FailedCount > 0 && (
                                    <Badge variant="destructive">
                                        {t("summaryFailed")}: {copyResult.FailedCount}
                                    </Badge>
                                )}
                                <Badge variant="secondary">
                                    {t("summaryRowsCopied")}: {copyResult.TotalRowsCopied}
                                </Badge>
                            </div>
                        </div>

                        <div className="max-h-80 overflow-y-auto rounded-lg border border-border">
                            <Table>
                                <TableHeader>
                                    <TableHeadRow>
                                        <TableHead>{t("colTableName")}</TableHead>
                                        <TableHead>{t("colStatus")}</TableHead>
                                        <TableHead>{t("colRowsCopied")}</TableHead>
                                        <TableHead>{t("colMessage")}</TableHead>
                                    </TableHeadRow>
                                </TableHeader>
                                <TableBody>
                                    {copyResult.Details.map(detail => (
                                        <TableRow key={detail.Name}>
                                            <TableCell className="font-medium">{detail.Name}</TableCell>
                                            <TableCell>
                                                {detail.Status === CopyTableStatus.Success && (
                                                    <Badge variant="default">{t("statusSuccess")}</Badge>
                                                )}
                                                {detail.Status === CopyTableStatus.Skipped && (
                                                    <Badge variant="outline">{t("statusSkipped")}</Badge>
                                                )}
                                                {detail.Status === CopyTableStatus.Failed && (
                                                    <Badge variant="destructive">{t("statusFailed")}</Badge>
                                                )}
                                            </TableCell>
                                            <TableCell>{detail.RowsCopied}</TableCell>
                                            <TableCell className="text-xs text-muted-foreground">
                                                {detail.Message || "-"}
                                            </TableCell>
                                        </TableRow>
                                    ))}
                                </TableBody>
                            </Table>
                        </div>
                    </div>
                )}
            </div>
        </InternalPage>
    );
};
