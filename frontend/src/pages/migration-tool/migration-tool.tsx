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

import { useLazyQuery, useMutation, useQuery } from "@apollo/client/react";
import {
    Badge,
    Button,
    Checkbox,
    cn,
    Input,
    Label,
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
    IconDownload as ArrowDownTrayIcon,
    IconFolder as FolderOpenIcon,
    IconInfo as InformationCircleIcon,
    IconPlay as PlayIcon,
    IconTerminal as CommandLineIcon,
} from "@clidey/ux";
import {
    ExecuteFlywayDocument,
    type ExecuteFlywayMutation,
    FlywayStatusDocument,
    InspectFlywayDirectoryDocument,
    InstallFlywayDocument,
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
import { selectDirectory } from "../../services/desktop";
import { useAppSelector } from "../../store/hooks";
import type { LocalLoginProfile } from "../../store/auth";
import { isDesktopApp } from "../../utils/external-links";

type RemoteSourceProfile = SourceProfilesQuery["SourceProfiles"][number];
type FlywayExecResult = ExecuteFlywayMutation["ExecuteFlyway"];

const LOCAL_PROJECT_KEY = "__local__";
const STORAGE_KEY_DIRECTORY = "whodb.flyway.lastDirectory";
const SUPPORTED_FLYWAY_TYPES = new Set(["Postgres", "MySQL", "MariaDB", "ClickHouse", "Sqlite3", "DuckDB"]);

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

function formatBytes(bytes: number): string {
    if (bytes < 1024) {
        return `${bytes} B`;
    }
    if (bytes < 1024 * 1024) {
        return `${(bytes / 1024).toFixed(1)} KB`;
    }
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

/**
 * MigrationToolPage provides one-click Flyway CLI installation, migration directory scanning,
 * and execution of Flyway migrations against a selected project and database.
 */
export const MigrationToolPage: FC = () => {
    const { t } = useTranslation("pages/migration-tool");
    const current = useAppSelector(state => state.auth.current);
    const localProfiles = useAppSelector(state => state.auth.profiles);
    const reduxSchema = useAppSelector(state => state.database.schema);

    const { data: statusData, loading: statusLoading, refetch: refetchStatus } = useQuery(FlywayStatusDocument, {
        fetchPolicy: "network-only",
    });
    const [installFlywayMutation, { loading: installing }] = useMutation(InstallFlywayDocument);
    const [inspectDirectoryQuery, { data: directoryData, loading: scanningDirectory }] = useLazyQuery(
        InspectFlywayDirectoryDocument,
        { fetchPolicy: "network-only" }
    );
    const [executeFlywayMutation, { loading: executing }] = useMutation(ExecuteFlywayDocument);

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

    const [directory, setDirectory] = useState<string>(() => {
        try {
            return localStorage.getItem(STORAGE_KEY_DIRECTORY) ?? "";
        } catch {
            return "";
        }
    });
    const [installOutput, setInstallOutput] = useState<string>("");
    const [baselineOnMigrate, setBaselineOnMigrate] = useState<boolean>(true);
    const [validateOnMigrate, setValidateOnMigrate] = useState<boolean>(true);
    const [outOfOrder, setOutOfOrder] = useState<boolean>(false);
    const [activeAction, setActiveAction] = useState<string>("");
    const [executionResult, setExecutionResult] = useState<FlywayExecResult | null>(null);

    const flywayStatus = statusData?.FlywayStatus;

    // Build list of all available database instances grouped by project
    const allInstances = useMemo<TargetInstanceOption[]>(() => {
        const byId = new Map<string, TargetInstanceOption>();
        const remoteProfiles: RemoteSourceProfile[] = profilesData?.SourceProfiles ?? [];

        for (const profile of remoteProfiles) {
            if (!SUPPORTED_FLYWAY_TYPES.has(profile.Type)) {
                continue;
            }
            const projectName = profile.ProjectName || getRecordValue(profile.Values, ["ProjectName", "ProjectCode"]);
            const envName = getRecordValue(profile.Values, ["EnvironmentName", "EnvironmentCode"]);
            const dbName = getRecordValue(profile.Values, ["Database"]) ?? "";
            const hostName = getRecordValue(profile.Values, ["Hostname"]) ?? "";
            const rawAlias = profile.Alias || profile.Id;
            const displayName = hostName && !rawAlias.includes(hostName)
                ? `[${profile.Type}] ${rawAlias} (${hostName})`
                : `[${profile.Type}] ${rawAlias}`;

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
            if (!SUPPORTED_FLYWAY_TYPES.has(local.Type) || byId.has(local.Id)) {
                continue;
            }
            const projectName = local.ProjectName || getRecordValue(local.Advanced, ["ProjectName", "ProjectCode"]);
            const envName = getRecordValue(local.Advanced, ["EnvironmentName", "EnvironmentCode"]);
            byId.set(local.Id, {
                id: local.Id,
                displayName: `[${local.Type}] ${formatLocalProfileName(local)}`,
                projectKey: projectName || LOCAL_PROJECT_KEY,
                projectLabel: projectName || t("localConnectionsGroup"),
                environmentLabel: envName,
                sourceType: local.Type,
                defaultDatabase: local.Database ?? "",
                isRemoteProfile: Boolean(local.Saved),
                localProfile: local,
            });
        }

        if (current && SUPPORTED_FLYWAY_TYPES.has(current.Type) && !byId.has(current.Id)) {
            const projectName = current.ProjectName || getRecordValue(current.Advanced, ["ProjectName", "ProjectCode"]);
            const envName = getRecordValue(current.Advanced, ["EnvironmentName", "EnvironmentCode"]);
            byId.set(current.Id, {
                id: current.Id,
                displayName: `[${current.Type}] ${formatLocalProfileName(current)}`,
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
    }, [current, localProfiles, profilesData?.SourceProfiles, t]);

    const projectOptions = useMemo(() => {
        const map = new Map<string, string>();
        for (const inst of allInstances) {
            if (!map.has(inst.projectKey)) {
                map.set(inst.projectKey, inst.projectLabel);
            }
        }
        return Array.from(map.entries()).map(([key, label]) => ({ key, label }));
    }, [allInstances]);

    const [selectedProjectKey, setSelectedProjectKey] = useState<string>("");
    const [selectedInstanceId, setSelectedInstanceId] = useState<string>("");
    const [targetDatabases, setTargetDatabases] = useState<string[]>([]);
    const [selectedTargetDatabase, setSelectedTargetDatabase] = useState<string>("");
    const [targetSchemas, setTargetSchemas] = useState<string[]>([]);
    const [selectedTargetSchema, setSelectedTargetSchema] = useState<string>("");

    const instancesInProject = useMemo(() => {
        if (!selectedProjectKey) {
            return allInstances;
        }
        return allInstances.filter(inst => inst.projectKey === selectedProjectKey);
    }, [allInstances, selectedProjectKey]);

    const selectedInstance = useMemo(
        () => allInstances.find(inst => inst.id === selectedInstanceId),
        [allInstances, selectedInstanceId]
    );

    const {
        supportsDatabaseSwitching,
        supportsSchema,
        usesDatabaseInsteadOfSchema,
    } = useSourceContract(selectedInstance?.sourceType ?? current?.Type);

    // Default project and instance to current active connection
    useEffect(() => {
        if (allInstances.length === 0) {
            return;
        }
        if (!selectedProjectKey || !projectOptions.some(p => p.key === selectedProjectKey)) {
            const currentInstance = current ? allInstances.find(i => i.id === current.Id) : undefined;
            const initialProject = currentInstance?.projectKey ?? projectOptions[0]?.key ?? "";
            setSelectedProjectKey(initialProject);
        }
    }, [allInstances, current, projectOptions, selectedProjectKey]);

    useEffect(() => {
        if (instancesInProject.length === 0) {
            setSelectedInstanceId("");
            return;
        }
        if (!selectedInstanceId || !instancesInProject.some(i => i.id === selectedInstanceId)) {
            const currentMatch = current ? instancesInProject.find(i => i.id === current.Id) : undefined;
            setSelectedInstanceId(currentMatch?.id ?? instancesInProject[0].id);
        }
    }, [current, instancesInProject, selectedInstanceId]);

    const buildTargetInput = useCallback(
        (instance: TargetInstanceOption | undefined, databaseOverride?: string): SourceTargetConnectionInput | null => {
            if (!instance) {
                return null;
            }
            const dbName = databaseOverride !== undefined ? databaseOverride : selectedTargetDatabase || instance.defaultDatabase;
            if (instance.isRemoteProfile) {
                return {
                    ProfileId: instance.id,
                    Database: dbName || undefined,
                };
            }
            const local = instance.localProfile;
            if (!local) {
                return {
                    ProfileId: instance.id,
                    Database: dbName || undefined,
                };
            }
            const values = [
                { Key: "Hostname", Value: local.Hostname },
                { Key: "Username", Value: local.Username },
                { Key: "Password", Value: local.Password },
                { Key: "Database", Value: dbName || local.Database },
                ...(local.Advanced ?? []),
            ];
            return {
                ProfileId: local.Id,
                SourceType: local.Type,
                Database: dbName || local.Database || undefined,
                Values: values,
            };
        },
        [selectedTargetDatabase]
    );

    // Load target databases when instance changes
    useEffect(() => {
        if (!selectedInstance) {
            setTargetDatabases([]);
            setSelectedTargetDatabase("");
            return;
        }

        const defaultDb = (current && selectedInstance.id === current.Id && current.Database)
            ? current.Database
            : selectedInstance.defaultDatabase;
        setSelectedTargetDatabase(defaultDb);

        if (!supportsDatabaseSwitching && !usesDatabaseInsteadOfSchema) {
            setTargetDatabases(defaultDb ? [defaultDb] : []);
            return;
        }

        const input = buildTargetInput(selectedInstance, defaultDb);
        if (!input) {
            return;
        }

        fetchTargetDatabases({ variables: { target: input } })
            .then(res => {
                const list = res.data?.SourceTargetDatabases ?? [];
                setTargetDatabases(list);
                if (list.length > 0) {
                    setSelectedTargetDatabase(prev => (prev && list.includes(prev) ? prev : (defaultDb && list.includes(defaultDb) ? defaultDb : list[0])));
                }
            })
            .catch(() => {
                setTargetDatabases(defaultDb ? [defaultDb] : []);
            });
    }, [selectedInstance, supportsDatabaseSwitching, usesDatabaseInsteadOfSchema, fetchTargetDatabases]);

    // Load target schemas when target database changes
    useEffect(() => {
        if (!selectedInstance) {
            setTargetSchemas([]);
            setSelectedTargetSchema("");
            return;
        }
        if (usesDatabaseInsteadOfSchema || !supportsSchema) {
            setTargetSchemas([]);
            setSelectedTargetSchema("");
            return;
        }

        const input = buildTargetInput(selectedInstance, selectedTargetDatabase);
        if (!input) {
            return;
        }

        fetchTargetSchemas({ variables: { target: input } })
            .then(res => {
                const list = res.data?.SourceTargetSchemas ?? [];
                setTargetSchemas(list);
                if (list.length > 0) {
                    const preferred = (current && selectedInstance.id === current.Id && reduxSchema && list.includes(reduxSchema))
                        ? reduxSchema
                        : list.includes("public")
                            ? "public"
                            : list[0];
                    setSelectedTargetSchema(prev => (prev && list.includes(prev) ? prev : preferred));
                }
            })
            .catch(() => {
                setTargetSchemas([]);
            });
    }, [selectedInstance, selectedTargetDatabase, supportsSchema, usesDatabaseInsteadOfSchema, fetchTargetSchemas]);

    // Scan initial directory if saved in localStorage
    useEffect(() => {
        if (directory.trim()) {
            void inspectDirectoryQuery({ variables: { directory: directory.trim() } });
        }
    }, []);

    const handleInstallFlyway = useCallback(async () => {
        setInstallOutput("");
        try {
            const res = await installFlywayMutation();
            const result = res.data?.InstallFlyway;
            if (!result) {
                return;
            }
            if (result.Output) {
                setInstallOutput(result.Output);
            }
            await refetchStatus();
            if (result.Success) {
                toast.success(t("installSuccess"));
            } else {
                toast.error(t("installFailed", { error: result.Error ?? "" }));
            }
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            toast.error(t("installFailed", { error: msg }));
        }
    }, [installFlywayMutation, refetchStatus, t]);

    const handleScanDirectory = useCallback((dirToScan?: string) => {
        const targetDir = (dirToScan ?? directory).trim();
        if (!targetDir) {
            return;
        }
        try {
            localStorage.setItem(STORAGE_KEY_DIRECTORY, targetDir);
        } catch {
            // ignore storage errors
        }
        void inspectDirectoryQuery({ variables: { directory: targetDir } });
    }, [directory, inspectDirectoryQuery]);

    const handleBrowseDirectory = useCallback(async () => {
        const chosen = await selectDirectory();
        if (chosen) {
            setDirectory(chosen);
            handleScanDirectory(chosen);
        }
    }, [handleScanDirectory]);

    const handleResetToCurrentConnection = useCallback(() => {
        if (!current) {
            return;
        }
        const inst = allInstances.find(i => i.id === current.Id);
        if (inst) {
            setSelectedProjectKey(inst.projectKey);
            setSelectedInstanceId(inst.id);
            setSelectedTargetDatabase(current.Database ?? inst.defaultDatabase);
            if (reduxSchema) {
                setSelectedTargetSchema(reduxSchema);
            }
        }
    }, [allInstances, current, reduxSchema]);

    const handleRunFlywayAction = useCallback(async (action: string) => {
        if (!selectedInstance) {
            return;
        }
        const trimmedDir = directory.trim();
        if (!trimmedDir) {
            toast.error(t("directoryPlaceholder"));
            return;
        }
        const targetInput = buildTargetInput(selectedInstance, selectedTargetDatabase);
        if (!targetInput) {
            return;
        }

        setActiveAction(action);
        try {
            const res = await executeFlywayMutation({
                variables: {
                    input: {
                        Target: targetInput,
                        TargetSchema: (!usesDatabaseInsteadOfSchema && supportsSchema && selectedTargetSchema)
                            ? selectedTargetSchema
                            : undefined,
                        Directory: trimmedDir,
                        Action: action,
                        BaselineOnMigrate: baselineOnMigrate,
                        ValidateOnMigrate: validateOnMigrate,
                        OutOfOrder: outOfOrder,
                    },
                },
            });
            const data = res.data?.ExecuteFlyway;
            if (data) {
                setExecutionResult(data);
                if (data.Success) {
                    toast.success(t("migrateSuccess", { action }));
                } else {
                    toast.error(t("migrateFailed", { action, error: data.Error ?? "" }));
                }
            }
        } catch (err) {
            const msg = err instanceof Error ? err.message : String(err);
            toast.error(t("migrateFailed", { action, error: msg }));
        } finally {
            setActiveAction("");
        }
    }, [
        baselineOnMigrate,
        buildTargetInput,
        directory,
        executeFlywayMutation,
        outOfOrder,
        selectedInstance,
        selectedTargetDatabase,
        selectedTargetSchema,
        supportsSchema,
        t,
        usesDatabaseInsteadOfSchema,
        validateOnMigrate,
    ]);

    const preview = directoryData?.InspectFlywayDirectory;
    const files = preview?.Files ?? [];
    const canExecute = Boolean(
        flywayStatus?.Installed &&
        selectedInstance &&
        directory.trim() &&
        !executing
    );

    const renderMigrationTypeBadge = (type: string) => {
        switch (type) {
            case "VERSIONED":
                return <Badge variant="default">{t("typeVersioned")}</Badge>;
            case "REPEATABLE":
                return <Badge variant="secondary">{t("typeRepeatable")}</Badge>;
            case "UNDO":
                return <Badge variant="outline">{t("typeUndo")}</Badge>;
            default:
                return <Badge variant="outline">{t("typeSql")}</Badge>;
        }
    };

    return (
        <InternalPage routes={[InternalRoutes.MigrationTool]}>
            <div className="flex flex-col gap-5 w-full max-w-7xl mx-auto pb-8">
                {/* Page Header */}
                <div className="flex flex-col gap-1">
                    <div className="flex items-center gap-2">
                        <CommandLineIcon className="w-5 h-5 text-primary" />
                        <h1 className="text-xl font-semibold tracking-tight">{t("title")}</h1>
                    </div>
                    <p className="text-xs text-muted-foreground">{t("subtitle")}</p>
                </div>

                {/* 1. Flyway CLI Status & One-Click Install */}
                <div className="rounded-xl border border-border bg-card p-4 flex flex-col gap-3 shadow-xs">
                    <div className="flex flex-wrap items-center justify-between gap-3">
                        <div className="flex items-center gap-3">
                            <span className="text-sm font-medium">{t("flywaySectionTitle")}</span>
                            {statusLoading ? (
                                <Badge variant="outline">...</Badge>
                            ) : flywayStatus?.Installed ? (
                                <Badge variant="default" className="gap-1">
                                    <CheckCircleIcon className="w-3.5 h-3.5" />
                                    {t("statusInstalled")}
                                </Badge>
                            ) : (
                                <Badge variant="destructive">{t("statusNotInstalled")}</Badge>
                            )}
                            {flywayStatus?.Installed && flywayStatus.Version && (
                                <span className="text-xs text-muted-foreground font-mono">
                                    {t("versionLabel")}: {flywayStatus.Version}
                                </span>
                            )}
                        </div>
                        <div className="flex items-center gap-2">
                            <Button
                                variant="outline"
                                size="sm"
                                className="h-8 text-xs gap-1.5"
                                onClick={() => { void refetchStatus(); }}
                                disabled={statusLoading || installing}
                            >
                                <ArrowPathIcon className={cn("w-3.5 h-3.5", statusLoading && "animate-spin")} />
                                {t("recheckButton")}
                            </Button>
                            {!flywayStatus?.Installed && (
                                <Button
                                    variant="default"
                                    size="sm"
                                    className="h-8 text-xs gap-1.5"
                                    onClick={() => { void handleInstallFlyway(); }}
                                    disabled={installing}
                                    data-testid="install-flyway-button"
                                >
                                    <ArrowDownTrayIcon className={cn("w-3.5 h-3.5", installing && "animate-bounce")} />
                                    {installing ? t("installingButton") : t("installButton")}
                                </Button>
                            )}
                        </div>
                    </div>

                    {flywayStatus?.Installed ? (
                        <div className="flex flex-wrap items-center gap-x-6 gap-y-1 text-xs text-muted-foreground">
                            {flywayStatus.BinaryPath && (
                                <div>
                                    <span className="font-medium text-foreground">{t("binaryPathLabel")}: </span>
                                    <code className="font-mono">{flywayStatus.BinaryPath}</code>
                                </div>
                            )}
                            {flywayStatus.InstallMethod && (
                                <div>
                                    <span className="font-medium text-foreground">{t("installMethodLabel")}: </span>
                                    <span className="uppercase">{flywayStatus.InstallMethod}</span>
                                </div>
                            )}
                        </div>
                    ) : (
                        <div className="flex items-center gap-2 text-xs text-amber-500">
                            <InformationCircleIcon className="w-4 h-4 shrink-0" />
                            <span>{t("notInstalledDesc")}</span>
                        </div>
                    )}

                    {installOutput && (
                        <pre className="mt-1 max-h-40 overflow-auto rounded-lg bg-muted/60 p-3 text-xs font-mono whitespace-pre-wrap break-all border border-border/60">
                            {installOutput}
                        </pre>
                    )}
                </div>

                {/* 2. Target Project & Database Selection + Migration Directory */}
                <div className="grid grid-cols-1 lg:grid-cols-12 gap-4">
                    {/* Left: Target Project & Database */}
                    <div className="lg:col-span-5 rounded-xl border border-border bg-card p-4 flex flex-col gap-3.5 shadow-xs">
                        <div className="flex items-center justify-between">
                            <span className="text-sm font-medium">{t("targetSectionTitle")}</span>
                            {current && SUPPORTED_FLYWAY_TYPES.has(current.Type) && (
                                <Button
                                    variant="ghost"
                                    size="sm"
                                    className="h-7 text-xs px-2"
                                    onClick={handleResetToCurrentConnection}
                                >
                                    {t("useCurrentTarget")}
                                </Button>
                            )}
                        </div>

                        {current && !SUPPORTED_FLYWAY_TYPES.has(current.Type) && allInstances.length === 0 && (
                            <div className="rounded-lg border border-amber-500/40 bg-amber-500/10 p-3 text-xs text-amber-500">
                                {t("unsupportedDbWarning", { type: current.Type })}
                            </div>
                        )}

                        <div className="grid grid-cols-1 gap-3">
                            {/* Project */}
                            <div className="flex flex-col gap-1.5">
                                <Label className="text-xs text-muted-foreground">{t("targetProjectLabel")}</Label>
                                <Select value={selectedProjectKey} onValueChange={setSelectedProjectKey}>
                                    <SelectTrigger className="w-full h-9 text-xs">
                                        <SelectValue placeholder={t("selectProjectPlaceholder")} />
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

                            {/* Database Instance */}
                            <div className="flex flex-col gap-1.5">
                                <Label className="text-xs text-muted-foreground">{t("targetInstanceLabel")}</Label>
                                <Select value={selectedInstanceId} onValueChange={setSelectedInstanceId}>
                                    <SelectTrigger className="w-full h-9 text-xs">
                                        <SelectValue placeholder={t("selectInstancePlaceholder")} />
                                    </SelectTrigger>
                                    <SelectContent>
                                        {instancesInProject.map(inst => (
                                            <SelectItem key={inst.id} value={inst.id}>
                                                {inst.displayName}
                                            </SelectItem>
                                        ))}
                                    </SelectContent>
                                </Select>
                            </div>

                            {/* Target Database */}
                            <div className="flex flex-col gap-1.5">
                                <Label className="text-xs text-muted-foreground">{t("targetDatabaseLabel")}</Label>
                                {targetDatabases.length > 0 ? (
                                    <Select
                                        value={selectedTargetDatabase}
                                        onValueChange={setSelectedTargetDatabase}
                                        disabled={targetDatabasesLoading}
                                    >
                                        <SelectTrigger className="w-full h-9 text-xs">
                                            <SelectValue placeholder={t("selectDatabasePlaceholder")} />
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
                                        onChange={e => setSelectedTargetDatabase(e.target.value)}
                                        placeholder={t("selectDatabasePlaceholder")}
                                        className="h-9 text-xs"
                                    />
                                )}
                            </div>

                            {/* Target Schema (if applicable, e.g. PostgreSQL) */}
                            {!usesDatabaseInsteadOfSchema && supportsSchema && (
                                <div className="flex flex-col gap-1.5">
                                    <Label className="text-xs text-muted-foreground">{t("targetSchemaLabel")}</Label>
                                    {targetSchemas.length > 0 ? (
                                        <Select
                                            value={selectedTargetSchema}
                                            onValueChange={setSelectedTargetSchema}
                                            disabled={targetSchemasLoading}
                                        >
                                            <SelectTrigger className="w-full h-9 text-xs">
                                                <SelectValue placeholder={t("selectSchemaPlaceholder")} />
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
                                            onChange={e => setSelectedTargetSchema(e.target.value)}
                                            placeholder={t("selectSchemaPlaceholder")}
                                            className="h-9 text-xs"
                                        />
                                    )}
                                </div>
                            )}
                        </div>
                    </div>

                    {/* Right: Migration Directory & Execution Controls */}
                    <div className="lg:col-span-7 rounded-xl border border-border bg-card p-4 flex flex-col justify-between gap-4 shadow-xs">
                        <div className="flex flex-col gap-3.5">
                            <span className="text-sm font-medium">{t("directorySectionTitle")}</span>

                            <div className="flex items-center gap-2">
                                <Input
                                    value={directory}
                                    onChange={e => setDirectory(e.target.value)}
                                    onKeyDown={e => {
                                        if (e.key === "Enter") {
                                            handleScanDirectory();
                                        }
                                    }}
                                    placeholder={t("directoryPlaceholder")}
                                    className="flex-1 h-9 text-xs font-mono"
                                    data-testid="flyway-directory-input"
                                />
                                {isDesktopApp() && (
                                    <Button
                                        variant="secondary"
                                        size="sm"
                                        className="h-9 text-xs gap-1.5 shrink-0"
                                        onClick={() => { void handleBrowseDirectory(); }}
                                    >
                                        <FolderOpenIcon className="w-3.5 h-3.5" />
                                        {t("browseDirectory")}
                                    </Button>
                                )}
                                <Button
                                    variant="outline"
                                    size="sm"
                                    className="h-9 text-xs gap-1.5 shrink-0"
                                    onClick={() => { handleScanDirectory(); }}
                                    disabled={!directory.trim() || scanningDirectory}
                                >
                                    <ArrowPathIcon className={cn("w-3.5 h-3.5", scanningDirectory && "animate-spin")} />
                                    {t("scanDirectory")}
                                </Button>
                            </div>

                            {/* Options */}
                            <div className="flex flex-col gap-2 pt-1">
                                <Label className="text-xs text-muted-foreground">{t("optionsTitle")}</Label>
                                <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
                                    <label className="flex items-center gap-2 text-xs cursor-pointer select-none">
                                        <Checkbox
                                            checked={baselineOnMigrate}
                                            onCheckedChange={val => setBaselineOnMigrate(Boolean(val))}
                                        />
                                        <span>{t("baselineOnMigrate")}</span>
                                    </label>
                                    <label className="flex items-center gap-2 text-xs cursor-pointer select-none">
                                        <Checkbox
                                            checked={validateOnMigrate}
                                            onCheckedChange={val => setValidateOnMigrate(Boolean(val))}
                                        />
                                        <span>{t("validateOnMigrate")}</span>
                                    </label>
                                    <label className="flex items-center gap-2 text-xs cursor-pointer select-none">
                                        <Checkbox
                                            checked={outOfOrder}
                                            onCheckedChange={val => setOutOfOrder(Boolean(val))}
                                        />
                                        <span>{t("outOfOrder")}</span>
                                    </label>
                                </div>
                            </div>
                        </div>

                        {/* Action Buttons */}
                        <div className="flex flex-wrap items-center gap-2 pt-2 border-t border-border/50">
                            <Button
                                variant="default"
                                size="sm"
                                className="h-9 px-4 text-xs gap-1.5"
                                disabled={!canExecute}
                                onClick={() => { void handleRunFlywayAction("migrate"); }}
                                data-testid="flyway-migrate-button"
                            >
                                <PlayIcon className="w-3.5 h-3.5" />
                                {activeAction === "migrate" ? t("runningAction", { action: "migrate" }) : t("actionMigrate")}
                            </Button>
                            <Button
                                variant="secondary"
                                size="sm"
                                className="h-9 text-xs gap-1.5"
                                disabled={!canExecute}
                                onClick={() => { void handleRunFlywayAction("info"); }}
                            >
                                {activeAction === "info" ? t("runningAction", { action: "info" }) : t("actionInfo")}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                className="h-9 text-xs gap-1.5"
                                disabled={!canExecute}
                                onClick={() => { void handleRunFlywayAction("validate"); }}
                            >
                                {activeAction === "validate" ? t("runningAction", { action: "validate" }) : t("actionValidate")}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                className="h-9 text-xs gap-1.5"
                                disabled={!canExecute}
                                onClick={() => { void handleRunFlywayAction("baseline"); }}
                            >
                                {activeAction === "baseline" ? t("runningAction", { action: "baseline" }) : t("actionBaseline")}
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                className="h-9 text-xs gap-1.5"
                                disabled={!canExecute}
                                onClick={() => { void handleRunFlywayAction("repair"); }}
                            >
                                {activeAction === "repair" ? t("runningAction", { action: "repair" }) : t("actionRepair")}
                            </Button>
                        </div>
                    </div>
                </div>

                {/* 3. Discovered Migration Files */}
                {preview && (
                    <div className="rounded-xl border border-border bg-card p-4 flex flex-col gap-3 shadow-xs">
                        <div className="flex items-center justify-between">
                            <div className="flex items-center gap-2">
                                <span className="text-sm font-medium">{t("directorySectionTitle")}</span>
                                <Badge variant="secondary">{t("filesFound", { count: files.length })}</Badge>
                            </div>
                            <code className="text-xs text-muted-foreground font-mono truncate max-w-md">
                                {preview.Directory}
                            </code>
                        </div>

                        {!preview.Exists ? (
                            <div className="text-xs text-destructive py-4">
                                {t("directoryNotExist", { error: preview.Error ?? "" })}
                            </div>
                        ) : files.length === 0 ? (
                            <div className="text-xs text-muted-foreground py-6 text-center">
                                {t("noFilesFound")}
                            </div>
                        ) : (
                            <div className="max-h-72 overflow-auto rounded-lg border border-border">
                                <Table>
                                    <TableHeader>
                                        <TableHeadRow>
                                            <TableHead className="w-28">{t("colVersion")}</TableHead>
                                            <TableHead className="w-32">{t("colType")}</TableHead>
                                            <TableHead>{t("colDescription")}</TableHead>
                                            <TableHead>{t("colFilename")}</TableHead>
                                            <TableHead className="w-24 text-right">{t("colSize")}</TableHead>
                                        </TableHeadRow>
                                    </TableHeader>
                                    <TableBody>
                                        {files.map(file => (
                                            <TableRow key={file.RelativePath}>
                                                <TableCell className="font-mono text-xs font-medium">
                                                    {file.Version}
                                                </TableCell>
                                                <TableCell>{renderMigrationTypeBadge(file.Type)}</TableCell>
                                                <TableCell className="text-xs">{file.Description}</TableCell>
                                                <TableCell className="font-mono text-xs text-muted-foreground">
                                                    {file.RelativePath}
                                                </TableCell>
                                                <TableCell className="font-mono text-xs text-right text-muted-foreground">
                                                    {formatBytes(file.SizeBytes)}
                                                </TableCell>
                                            </TableRow>
                                        ))}
                                    </TableBody>
                                </Table>
                            </div>
                        )}
                    </div>
                )}

                {/* 4. Execution Output Console */}
                {(executing || executionResult) && (
                    <div className="rounded-xl border border-border bg-card p-4 flex flex-col gap-3 shadow-xs">
                        <div className="flex flex-wrap items-center justify-between gap-2">
                            <div className="flex items-center gap-2">
                                <span className="text-sm font-medium">{t("executionOutputTitle")}</span>
                                {executing ? (
                                    <Badge variant="secondary">{t("runningAction", { action: activeAction })}</Badge>
                                ) : executionResult ? (
                                    <>
                                        <Badge variant={executionResult.Success ? "default" : "destructive"}>
                                            {executionResult.Success ? t("statusSuccess") : t("statusFailed")}
                                        </Badge>
                                        <span className="text-xs text-muted-foreground font-mono">
                                            {t("durationLabel", { ms: executionResult.DurationMs })}
                                        </span>
                                    </>
                                ) : null}
                            </div>
                        </div>

                        {executing ? (
                            <div className="py-8 flex items-center justify-center">
                                <Loading />
                            </div>
                        ) : executionResult ? (
                            <div className="flex flex-col gap-2">
                                {executionResult.Command && (
                                    <div className="rounded-md bg-muted/50 px-3 py-1.5 text-xs font-mono text-muted-foreground break-all border border-border/50">
                                        $ {executionResult.Command}
                                    </div>
                                )}
                                {executionResult.Error && !executionResult.Success && (
                                    <div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-xs text-destructive font-mono whitespace-pre-wrap break-all">
                                        {executionResult.Error}
                                    </div>
                                )}
                                {executionResult.Output && (
                                    <pre className="max-h-96 overflow-auto rounded-lg bg-neutral-950 text-neutral-100 p-4 text-xs font-mono whitespace-pre-wrap break-all border border-border">
                                        {executionResult.Output}
                                    </pre>
                                )}
                            </div>
                        ) : null}
                    </div>
                )}
            </div>
        </InternalPage>
    );
};
