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
    Dialog,
    DialogContent,
    DialogDescription,
    DialogFooter,
    DialogHeader,
    DialogTitle,
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
    IconChevronDown as ChevronDownIcon,
    IconDownload as ArrowDownTrayIcon,
    IconFolder as FolderOpenIcon,
    IconInfo as InformationCircleIcon,
    IconPlay as PlayIcon,
    IconTrash as TrashIcon,
} from "@clidey/ux";
import {
    ExecuteFlywayDocument,
    type ExecuteFlywayMutation,
    FlywayStatusDocument,
    InspectFlywayDirectoryDocument,
    InstallFlywayDocument,
    SourceMigrationDiagnosisDocument,
    RepairSourceMigrationsDocument,
    type SourceMigrationDiagnosisQuery,
    type RepairSourceMigrationsMutation,
    type SourceMigrationInput,
    SourceProfilesDocument,
    type SourceProfilesQuery,
    SourceTargetDatabasesDocument,
    SourceTargetSchemasDocument,
    type SourceTargetConnectionInput,
} from "@graphql";
import { type FC, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { InternalPage } from "../../components/page";
import { Loading } from "../../components/loading";
import { MigrationDuplicateExport } from "./migration-duplicate-export";
import { MigrationDuplicateCleanup } from "./migration-duplicate-cleanup";
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
const STORAGE_KEY_DIRECTORY_HISTORY = "whodb.flyway.directoryHistory";
const MAX_DIRECTORY_HISTORY = 20;
const SUPPORTED_FLYWAY_TYPES = new Set(["Postgres", "MySQL", "MariaDB", "ClickHouse", "Sqlite3", "DuckDB"]);

function loadDirectoryHistory(): string[] {
    try {
        const raw = localStorage.getItem(STORAGE_KEY_DIRECTORY_HISTORY);
        const parsed = raw ? (JSON.parse(raw) as unknown) : [];
        const list = Array.isArray(parsed)
            ? parsed.filter((item): item is string => typeof item === "string" && item.trim().length > 0).map(s => s.trim())
            : [];
        const last = localStorage.getItem(STORAGE_KEY_DIRECTORY)?.trim();
        if (last && !list.includes(last)) {
            list.unshift(last);
        }
        return Array.from(new Set(list)).slice(0, MAX_DIRECTORY_HISTORY);
    } catch {
        return [];
    }
}

function saveDirectoryHistoryList(list: string[]): void {
    try {
        localStorage.setItem(STORAGE_KEY_DIRECTORY_HISTORY, JSON.stringify(list));
    } catch {
        // ignore storage errors
    }
}

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
    const [diagnoseMigration, { loading: diagnosing }] = useLazyQuery(SourceMigrationDiagnosisDocument, { fetchPolicy: "network-only" });
    const [repairMigrations, { loading: repairing }] = useMutation(RepairSourceMigrationsDocument);
    const [diagnosisOpen, setDiagnosisOpen] = useState(false);
    const [diagnosis, setDiagnosis] = useState<SourceMigrationDiagnosisQuery["SourceMigrationDiagnosis"] | null>(null);
    const [diagnosisInput, setDiagnosisInput] = useState<SourceMigrationInput | null>(null);
    const [diagnosisTarget, setDiagnosisTarget] = useState("");
    const [diagnosisError, setDiagnosisError] = useState<string | null>(null);
    const [cleanupConfirmed, setCleanupConfirmed] = useState(false);
    const [repairResult, setRepairResult] = useState<RepairSourceMigrationsMutation["RepairSourceMigrations"] | null>(null);

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
    const [directoryHistory, setDirectoryHistory] = useState<string[]>(() => loadDirectoryHistory());
    const [directoryDropdownOpen, setDirectoryDropdownOpen] = useState(false);
    const directoryDropdownRef = useRef<HTMLDivElement>(null);
    const [filesOpen, setFilesOpen] = useState(false);

    const recordDirectoryHistory = useCallback((dir: string) => {
        const trimmed = dir.trim();
        if (!trimmed) {
            return;
        }
        try {
            localStorage.setItem(STORAGE_KEY_DIRECTORY, trimmed);
        } catch {
            // ignore storage errors
        }
        setDirectoryHistory(prev => {
            const next = [trimmed, ...prev.filter(item => item !== trimmed)].slice(0, MAX_DIRECTORY_HISTORY);
            saveDirectoryHistoryList(next);
            return next;
        });
    }, []);

    const handleRemoveDirectoryHistory = useCallback((dirToRemove: string) => {
        setDirectoryHistory(prev => {
            const next = prev.filter(item => item !== dirToRemove);
            saveDirectoryHistoryList(next);
            try {
                if (localStorage.getItem(STORAGE_KEY_DIRECTORY) === dirToRemove) {
                    localStorage.setItem(STORAGE_KEY_DIRECTORY, next[0] ?? "");
                }
            } catch {
                // ignore storage errors
            }
            return next;
        });
    }, []);

    useEffect(() => {
        if (!directoryDropdownOpen) {
            return;
        }
        const handleClickOutside = (event: MouseEvent) => {
            if (directoryDropdownRef.current && !directoryDropdownRef.current.contains(event.target as Node)) {
                setDirectoryDropdownOpen(false);
            }
        };
        document.addEventListener("mousedown", handleClickOutside);
        return () => {
            document.removeEventListener("mousedown", handleClickOutside);
        };
    }, [directoryDropdownOpen]);
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
        recordDirectoryHistory(targetDir);
        setFilesOpen(true);
        void inspectDirectoryQuery({ variables: { directory: targetDir } });
    }, [directory, inspectDirectoryQuery, recordDirectoryHistory]);

    const handleSelectHistoryDirectory = useCallback((selectedDir: string) => {
        const trimmed = selectedDir.trim();
        setDirectory(trimmed);
        setDirectoryDropdownOpen(false);
        if (trimmed) {
            recordDirectoryHistory(trimmed);
            void inspectDirectoryQuery({ variables: { directory: trimmed } });
        }
    }, [inspectDirectoryQuery, recordDirectoryHistory]);

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

    const handleDiagnose = useCallback(async () => {
        if (!selectedInstance || !directory.trim()) return;
        recordDirectoryHistory(directory.trim());
        const target = buildTargetInput(selectedInstance, selectedTargetDatabase);
        if (!target) return;
        const input: SourceMigrationInput = {
            Target: target,
            TargetSchema: !usesDatabaseInsteadOfSchema && supportsSchema ? selectedTargetSchema : undefined,
            Directory: directory.trim(),
            BaselineOnMigrate: baselineOnMigrate,
            OutOfOrder: outOfOrder,
            ValidateOnMigrate: true,
        };
        setDiagnosisInput(input);
        setDiagnosisTarget(`${selectedInstance.displayName} · ${selectedTargetDatabase}${input.TargetSchema ? ` · ${input.TargetSchema}` : ''}`);
        setDiagnosis(null);
        setDiagnosisError(null);
        setCleanupConfirmed(false);
        setRepairResult(null);
        setDiagnosisOpen(true);
        try {
            const response = await diagnoseMigration({ variables: { input } });
            setDiagnosis(response.data?.SourceMigrationDiagnosis ?? null);
        } catch (error) {
            setDiagnosisError(error instanceof Error ? error.message : String(error));
        }
    }, [baselineOnMigrate, buildTargetInput, diagnoseMigration, directory, outOfOrder, recordDirectoryHistory, selectedInstance, selectedTargetDatabase, selectedTargetSchema, supportsSchema, usesDatabaseInsteadOfSchema]);

    const handleRunFlywayAction = useCallback(async (action: string) => {
        if (!selectedInstance) {
            return;
        }
        const trimmedDir = directory.trim();
        if (!trimmedDir) {
            toast.error(t("directoryPlaceholder"));
            return;
        }
        recordDirectoryHistory(trimmedDir);
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
                    if (action === "migrate") {
                        void handleDiagnose();
                    }
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
        handleDiagnose,
        outOfOrder,
        recordDirectoryHistory,
        selectedInstance,
        selectedTargetDatabase,
        selectedTargetSchema,
        supportsSchema,
        t,
        usesDatabaseInsteadOfSchema,
        validateOnMigrate,
    ]);

    const handleDiagnosedRepair = useCallback(async () => {
        if (!diagnosisInput || !diagnosis?.Token || !cleanupConfirmed || repairing || repairResult) return;
        const structural = diagnosis.Issues.some(issue => issue.Recovery?.Safe === true);
        try {
            const response = await repairMigrations({ variables: { input: {
                Migration: diagnosisInput,
                Token: diagnosis.Token,
                ConfirmPartialChangesResolved: !structural,
                ConfirmPlanExecution: structural,
            } } });
            const result = response.data?.RepairSourceMigrations;
            if (result) {
                setRepairResult(result);
                const lastStep = result.Steps.at(-1);
                if (lastStep) setExecutionResult(lastStep);
            }
        } catch (error) {
            setDiagnosisError(error instanceof Error ? error.message : String(error));
        } finally {
            // The backend consumes the plan on its first confirmed use.
            setDiagnosis(previous => previous ? { ...previous, Token: null } : previous);
        }
    }, [cleanupConfirmed, diagnosis, diagnosisInput, repairMigrations, repairing, repairResult]);

    const preview = directoryData?.InspectFlywayDirectory;
    const hasStructureRecovery = diagnosis?.Issues.some(issue => issue.Recovery != null) ?? false;
    const recoveryStatusLabels = { MATCHED: t("recoveryMatched"), PENDING: t("recoveryPending"), CONFLICT: t("recoveryConflict") };
    const recoveryKindLabels = { CREATE_TABLE: t("recoveryCreateTable"), ADD_COLUMN: t("recoveryAddColumn"), CREATE_INDEX: t("recoveryCreateIndex") };
    const recoveryPhaseLabels: Record<string, string> = {
        repair: t("actionRepair"), validate: t("actionValidate"), migrate: t("actionMigrate"),
        "verify-original-checksum": t("recoveryVerifyChecksum"), "restore-checksum": t("recoveryRestoreChecksum"), "validate-final": t("recoveryFinalValidation"),
        "verify-structure": t("recoveryVerifyStructure"),
    };
    const files = preview?.Files ?? [];
    const canExecute = Boolean(
        flywayStatus?.Installed &&
        selectedInstance &&
        directory.trim() &&
        !executing && !diagnosing && !repairing
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
                                        onChange={e =>{  setSelectedTargetDatabase(e.target.value); }}
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
                                            onChange={e =>{  setSelectedTargetSchema(e.target.value); }}
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
                                <div className="relative flex-1 min-w-0" ref={directoryDropdownRef}>
                                    <Input
                                        value={directory}
                                        onChange={e => { setDirectory(e.target.value); }}
                                        onFocus={() => {
                                            if (directoryHistory.length > 0) {
                                                setDirectoryDropdownOpen(true);
                                            }
                                        }}
                                        onKeyDown={e => {
                                            if (e.key === "Enter") {
                                                setDirectoryDropdownOpen(false);
                                                handleScanDirectory();
                                            } else if (e.key === "Escape") {
                                                setDirectoryDropdownOpen(false);
                                            }
                                        }}
                                        placeholder={t("directoryPlaceholder")}
                                        className={cn("w-full h-9 text-xs font-mono", directoryHistory.length > 0 && "pr-8")}
                                        data-testid="flyway-directory-input"
                                    />
                                    {directoryHistory.length > 0 && (
                                        <button
                                            type="button"
                                            aria-label={t("directoryHistoryLabel")}
                                            data-testid="flyway-directory-history-trigger"
                                            onClick={() => { setDirectoryDropdownOpen(prev => !prev); }}
                                            className="absolute right-1.5 top-1/2 -translate-y-1/2 flex items-center justify-center w-6 h-6 rounded hover:bg-muted text-muted-foreground hover:text-foreground transition-colors"
                                        >
                                            <ChevronDownIcon className={cn("w-3.5 h-3.5 transition-transform", directoryDropdownOpen && "rotate-180")} />
                                        </button>
                                    )}
                                    {directoryDropdownOpen && directoryHistory.length > 0 && (
                                        <div
                                            className="absolute left-0 right-0 top-full mt-1 z-50 max-h-60 overflow-y-auto rounded-lg border border-border bg-popover text-popover-foreground shadow-md p-1"
                                            data-testid="flyway-directory-history-dropdown"
                                        >
                                            {directoryHistory.map(historyPath => (
                                                <div
                                                    key={historyPath}
                                                    className={cn(
                                                        "flex items-center justify-between gap-2 px-2.5 py-1.5 rounded-md text-xs font-mono hover:bg-accent hover:text-accent-foreground cursor-pointer group",
                                                        historyPath === directory.trim() && "bg-accent/60 font-medium"
                                                    )}
                                                    onClick={() => { handleSelectHistoryDirectory(historyPath); }}
                                                >
                                                    <span className="truncate flex-1 min-w-0" title={historyPath}>
                                                        {historyPath}
                                                    </span>
                                                    <button
                                                        type="button"
                                                        aria-label={t("removeDirectoryHistory")}
                                                        title={t("removeDirectoryHistory")}
                                                        data-testid="delete-directory-history-item"
                                                        onClick={e => {
                                                            e.stopPropagation();
                                                            handleRemoveDirectoryHistory(historyPath);
                                                        }}
                                                        className="shrink-0 p-1 rounded text-muted-foreground hover:text-destructive hover:bg-destructive/10 transition-colors"
                                                    >
                                                        <TrashIcon className="w-3.5 h-3.5" />
                                                    </button>
                                                </div>
                                            ))}
                                        </div>
                                    )}
                                </div>
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
                                {preview && (
                                    <Button variant="outline" size="sm" className="h-9 text-xs shrink-0"
                                        onClick={() => { setFilesOpen(true); }} data-testid="view-migration-files">
                                        {t("viewFiles")} ({files.length})
                                    </Button>
                                )}
                            </div>

                            {/* Options */}
                            <div className="flex flex-col gap-2 pt-1">
                                <Label className="text-xs text-muted-foreground">{t("optionsTitle")}</Label>
                                <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
                                    <label className="flex items-center gap-2 text-xs cursor-pointer select-none">
                                        <Checkbox
                                            checked={baselineOnMigrate}
                                            onCheckedChange={val =>{  setBaselineOnMigrate(Boolean(val)); }}
                                        />
                                        <span>{t("baselineOnMigrate")}</span>
                                    </label>
                                    <label className="flex items-center gap-2 text-xs cursor-pointer select-none">
                                        <Checkbox
                                            checked={validateOnMigrate}
                                            onCheckedChange={val =>{  setValidateOnMigrate(Boolean(val)); }}
                                        />
                                        <span>{t("validateOnMigrate")}</span>
                                    </label>
                                    <label className="flex items-center gap-2 text-xs cursor-pointer select-none">
                                        <Checkbox
                                            checked={outOfOrder}
                                            onCheckedChange={val =>{  setOutOfOrder(Boolean(val)); }}
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
                                data-testid="flyway-info-button"
                            >
                                {activeAction === "info" ? t("runningAction", { action: "info" }) : t("actionInfo")}
                            </Button>
                        </div>
                    </div>
                </div>

                <Dialog open={filesOpen} onOpenChange={setFilesOpen}>
                    <DialogContent className="flex max-h-[85vh] w-[calc(100vw-2rem)] flex-col overflow-hidden sm:max-w-[1100px]" data-testid="migration-files-dialog">
                        <DialogHeader className="shrink-0 pr-6">
                            <DialogTitle>{t("directorySectionTitle")}</DialogTitle>
                            <DialogDescription className="break-all font-mono text-xs">
                                {preview?.Directory ?? directory}
                            </DialogDescription>
                        </DialogHeader>
                        {scanningDirectory ? <Loading /> : preview && <>
                            <Badge variant="secondary" className="w-fit shrink-0">{t("filesFound", { count: files.length })}</Badge>
                            {!preview.Exists || preview.Error ? (
                                <div className="text-xs text-destructive py-4">
                                    {t("directoryNotExist", { error: preview.Error ?? "" })}
                                </div>
                            ) : files.length === 0 ? (
                                <div className="text-xs text-muted-foreground py-6 text-center">
                                    {t("noFilesFound")}
                                </div>
                            ) : (
                                <div className="min-h-0 flex-1 overflow-auto rounded-lg border border-border">
                                    <Table className="min-w-[800px]">
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
                        </>}
                    </DialogContent>
                </Dialog>

                <Dialog open={diagnosisOpen} onOpenChange={open => { if (!repairing) setDiagnosisOpen(open); }}>
                    <DialogContent className="flex max-h-[85vh] w-[calc(100vw-2rem)] flex-col overflow-hidden sm:max-w-3xl" data-testid="migration-diagnosis-dialog">
                        <DialogHeader className="shrink-0 pr-6">
                            <DialogTitle>{t("diagnoseAndRepair")}</DialogTitle>
                            <DialogDescription className="break-all">{diagnosisTarget}</DialogDescription>
                        </DialogHeader>
                        <div className="min-h-0 overflow-auto space-y-4">
                            <p className="text-xs text-muted-foreground">{t("diagnosisScope")}</p>
                            {diagnosing && <div role="status"><Loading /><p className="mt-2 text-sm">{t("diagnosingMigrations")}</p></div>}
                            {(diagnosisError ?? diagnosis?.Error) && <p role="alert" className="text-sm text-destructive">{diagnosisError ?? diagnosis?.Error}</p>}
                            {diagnosis?.Healthy && <p className="text-sm">{t("diagnosisHealthy")}</p>}
                            {diagnosis && !diagnosis.Healthy && !diagnosis.Error && <>
                                <p className="text-sm text-muted-foreground">{diagnosis.RepairAllowed ? hasStructureRecovery ? t("structureRecoveryReview") : t("diagnosisReviewNeeded") : t("diagnosisBlocked")}</p>
                                {diagnosis.Issues.map(issue => <div key={`${issue.Version}-${issue.Script}-${issue.Code}`} className="space-y-2 rounded-md border border-border p-3">
                                    <div className="flex flex-wrap items-center gap-2 text-sm"><Badge variant="outline">{issue.Version}</Badge><code className="break-all">{issue.Script}</code></div>
                                    <p className="whitespace-pre-wrap break-words text-xs text-muted-foreground">{issue.Message}</p>
                                    {issue.Recovery && <div className="space-y-2" data-testid="migration-structure-plan">
                                        <p className="text-sm">{t("structureRecoverySummary", { matched: issue.Recovery.Matched, pending: issue.Recovery.Pending })}</p>
                                        {issue.Recovery.Error && <p role="alert" className="text-sm text-destructive">{issue.Recovery.Error}</p>}
                                        <ul className="divide-y divide-border">
                                            {issue.Recovery.Steps.map(step => <li key={`${step.Kind}-${step.Object}`} className="py-2 space-y-1">
                                                <div className="flex flex-wrap items-center gap-2 text-xs">
                                                    <Badge variant={step.Status === "CONFLICT" ? "destructive" : "outline"}>{recoveryStatusLabels[step.Status]}</Badge>
                                                    <span>{recoveryKindLabels[step.Kind]}</span><code className="break-all">{step.Object}</code>
                                                </div>
                                                <p className="text-xs text-muted-foreground">{step.Reason}</p>
                                                {step.DuplicateDataAvailable && diagnosisInput && <div className="flex flex-wrap gap-2">
                                                    <MigrationDuplicateExport input={diagnosisInput} version={issue.Version} object={step.Object} />
                                                    <MigrationDuplicateCleanup input={diagnosisInput} version={issue.Version} object={step.Object} onCleaned={() => {void handleDiagnose();}} />
                                                </div>}
                                            </li>)}
                                        </ul>
                                    </div>}
                                    {issue.SQL && <details><summary className="cursor-pointer text-sm">{t("reviewMigrationSQL")}</summary><pre className="mt-2 max-h-64 overflow-auto rounded-md bg-muted p-3 text-xs">{issue.SQL}</pre></details>}
                                </div>)}
                                {diagnosis.RepairAllowed && !repairResult && <label className="flex items-start gap-3 text-sm">
                                    <Checkbox checked={cleanupConfirmed} onCheckedChange={value => { setCleanupConfirmed(value === true); }} disabled={repairing} data-testid="confirm-migration-cleanup" />
                                    <span>{hasStructureRecovery ? t("confirmStructureRecovery") : t("confirmMigrationCleanup")}</span>
                                </label>}
                            </>}
                            {repairing && <p role="status" className="text-sm">{t("repairingMigrations")}</p>}
                            {repairResult && <div className="space-y-3" data-testid="migration-repair-result">
                                <p role="status" className={cn("text-sm", !repairResult.Success && "text-destructive")}>{repairResult.Success ? t("repairMigrationsSuccess") : t("repairMigrationsStopped")}</p>
                                {repairResult.Error && <p className="text-sm text-destructive">{repairResult.Error}</p>}
                                {repairResult.Steps.map(step => <details key={step.Action} open={!step.Success}>
                                    <summary className="cursor-pointer text-sm">{recoveryPhaseLabels[step.Action]} · {step.Success ? t("statusSuccess") : t("statusFailed")}</summary>
                                    <pre className="mt-2 max-h-64 overflow-auto rounded-md bg-muted p-3 text-xs whitespace-pre-wrap break-all">{step.Output || step.Error}</pre>
                                </details>)}
                            </div>}
                        </div>
                        <DialogFooter className="shrink-0">
                            {diagnosis?.RepairAllowed && !repairResult && <Button disabled={!cleanupConfirmed || !diagnosis.Token || repairing} onClick={() => { void handleDiagnosedRepair(); }} data-testid="repair-diagnosed-migrations">{repairing ? t("repairingMigrations") : hasStructureRecovery ? t("applyStructureRecovery") : t("repairAndRetryMigrations")}</Button>}
                            {(repairResult != null || diagnosisError != null || (diagnosis != null && !diagnosis.Token && !diagnosis.Healthy)) && <Button variant="outline" disabled={repairing || diagnosing} onClick={() => { void handleDiagnose(); }}>{t("diagnoseAgain")}</Button>}
                        </DialogFooter>
                    </DialogContent>
                </Dialog>

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
