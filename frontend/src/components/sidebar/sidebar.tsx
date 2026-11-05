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

import {skipToken, useQuery} from "@apollo/client/react";
import {
    Button,
    cn,
    CommandItem,
    Dialog,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
    Sheet,
    SheetContent,
    SheetTitle,
    Sidebar as SidebarComponent,
    SidebarContent,
    SidebarGroup,
    SidebarHeader,
    SidebarMenu,
    SidebarMenuButton,
    SidebarMenuItem,
    SidebarSeparator,
    SidebarTrigger,
    useSidebar,
    IconCloud as CloudIcon,
    IconSettings as CogIcon,
    IconTerminal as CommandLineIcon,
    IconCopy as DocumentDuplicateIcon,
    IconPlusCircle as PlusCircleIcon,
    IconApp as RectangleGroupIcon,
    IconTable as TableCellsIcon,
    IconClose as XMarkIcon,
    SearchSelect,
} from '@clidey/ux';
import {
    GetSchemaDocument,
    GetSslStatusDocument,
    SourceProfileLabelStrategy,
    SourceFieldOptionsDocument,
    SourceProfilesDocument,
} from '@graphql';
import {useTranslation} from '@/hooks/use-translation';
import {VisuallyHidden} from "@radix-ui/react-visually-hidden";
import type {FC, LazyExoticComponent, ReactElement, ReactNode} from "react";
import { Suspense, useCallback, useEffect, useMemo, useRef, useState} from "react";
import {useDispatch} from "react-redux";
import {Link, useLocation, useNavigate} from "react-router-dom";
import logoImage from "../../../public/images/logo.svg";
import {ANALYTICS_EVENTS} from "../../config/analytics-events";
import {extensions, featureFlags, getAppName} from "../../config/features";
import {countBucket, trackPlatformFunnel} from "../../config/frontend-analytics";
import {InternalRoutes} from "../../config/routes";
import {LoginForm} from "../../pages/auth/login";
import type { LocalLoginProfile} from "../../store/auth";
import {AuthActions} from "../../store/auth";
import {DatabaseActions} from "../../store/database";
import {useAppSelector} from "../../store/hooks";
import {getComponent} from "../../config/component-registry";
import { findSourceTypeItem, type SourceTypeItem } from "../../config/source-types";
import type {PlatformFunnelTrigger} from "../../utils/platform-funnel";
import {dismissBackupHint, hasDismissedBackupHint} from "../../utils/platform-hints";
import {ph} from "../../utils/privacy";
import {useSourceContract} from "../../hooks/useSourceContract";
import {useSourceTypeItems} from "../../hooks/useSourceCatalog";
import {PlatformExplainerDialog} from "./platform-explainer-dialog";
import {Icons} from "../icons";
import {Loading} from "../loading";
import {DatabaseIconWithBadge} from "../database-icon-with-badge";
import {WhoDBChatIcon} from "../whodb-chat-icon";
import {useProfileSwitch} from "@/hooks/use-profile-switch";
import {buildSourceSchemaQuery} from "@/utils/source-refs";
import {createProfilePayloadFromSourceProfile} from "@/utils/source-credentials";

const DB_TYPE_DISPLAY_NAMES: Record<string, string> = {
    Redis: 'Redis',
    MySQL: 'MySQL',
    Postgres: 'PostgreSQL',
    TDengine: 'TDengine',
    ClickHouse: 'ClickHouse',
    ElasticSearch: 'Elasticsearch',
    OpenObserve: 'OpenObserve',
    MongoDB: 'MongoDB',
    Sqlite3: 'SQLite',
    DuckDB: 'DuckDB',
    Memcached: 'Memcached',
};

const DB_TYPE_SORT_ORDER: Record<string, number> = {
    Redis: 1,
    MySQL: 2,
    Postgres: 3,
    TDengine: 4,
    ClickHouse: 5,
    ElasticSearch: 6,
    OpenObserve: 7,
    MongoDB: 8,
};

function resolveProfileProject(profile?: LocalLoginProfile): { id: string; name?: string; hasProject: boolean } {
    if (!profile) {
        return { id: "__local__", hasProject: false };
    }
    if (profile.ProjectId) {
        return {
            id: profile.ProjectId,
            name: profile.ProjectName ?? profile.ProjectId,
            hasProject: true,
        };
    }
    const raw = profile.DisplayName ?? profile.Id ?? "";
    const match = raw.match(/^\[([^\]]+)\]/);
    if (match?.[1]) {
        const parsed = match[1].trim();
        if (parsed) {
            return {
                id: parsed,
                name: parsed,
                hasProject: true,
            };
        }
    }
    return { id: "__local__", hasProject: false };
}

function getFriendlyDbTypeName(type: string): string {
    return DB_TYPE_DISPLAY_NAMES[type] ?? type;
}

function getProfileLabel(profile: LocalLoginProfile, item: SourceTypeItem | undefined) {
    const project = resolveProfileProject(profile);
    if (project.hasProject) {
        const port = profile.Values.find(value => value.Key === "Port")?.Value;
        const typeLabel = getFriendlyDbTypeName(profile.Type);
        const rawHost = profile.Hostname ?? "";
        let displayHost = rawHost;
        if (rawHost.includes("://")) {
            try {
                const parsed = new URL(rawHost);
                displayHost = parsed.hostname || rawHost;
            } catch {
                displayHost = rawHost;
            }
        }
        return displayHost
            ? `${typeLabel} (${displayHost}${port ? `:${port}` : ""})`
            : typeLabel;
    }
    if (profile.Saved) return profile.Id;
    if (item?.traits?.presentation.profileLabelStrategy === SourceProfileLabelStrategy.Hostname && profile.Hostname) {
        return profile.Hostname;
    }
    if (item?.traits?.presentation.profileLabelStrategy === SourceProfileLabelStrategy.Database && profile.Database) {
        return profile.Database;
    }
    if (profile.Hostname && profile.Username) {
        return `${profile.Hostname} [${profile.Username}]`;
    }
    if (profile.Database) {
        return profile.Database;
    }
    if (profile.Hostname) {
        return profile.Hostname;
    }
    return getFriendlyDbTypeName(profile.Type);
}

function getProfileIcon(profile: LocalLoginProfile) {
    return (Icons.Logos as Record<string, ReactElement>)[profile.Type];
}

/** Section header shown above each navigation group in the sidebar. */
export const NavSectionHeader: FC<{ label: string; open: boolean; disabled?: boolean }> = ({ label, open, disabled }) => {
    if (!open) return <div className="h-4" />;
    return (
        <div className="flex items-center gap-2 px-1 pt-3 pb-1">
            <span className="text-[10px] font-semibold uppercase tracking-[0.2em] text-neutral-600 select-none">
                {label}
            </span>
            {disabled && (
                <span className="text-[9px] uppercase tracking-wider text-neutral-700 border border-neutral-800 rounded px-1 py-0.5 leading-none">
                    Soon
                </span>
            )}
        </div>
    );
};

/** A single navigation item with active-state styling. */
export const NavItem: FC<{
    icon: React.ReactNode;
    label: string;
    path?: string;
    pathname: string;
    open: boolean;
    tooltip: string;
    disabled?: boolean;
    onClick?: () => void;
    "data-testid"?: string;
}> = ({ icon, label, path, pathname, open, tooltip, disabled, onClick, "data-testid": dataTestId }) => {
    const isActive = path != null && (
        pathname === path ||
        (path === InternalRoutes.Dashboard.ExploreStorageUnit.path && pathname === InternalRoutes.Dashboard.StorageUnit.path)
    );

    if (disabled) {
        return (
            <SidebarMenuItem>
                <SidebarMenuButton tooltip={tooltip} className="opacity-35 cursor-not-allowed pointer-events-none">
                    <span className="flex shrink-0 items-center justify-center text-neutral-600">{icon}</span>
                    {open && <span className="text-neutral-600 text-sm">{label}</span>}
                </SidebarMenuButton>
            </SidebarMenuItem>
        );
    }

    const inner = (
        <>
            <span className={cn(
                "flex shrink-0 items-center justify-center",
                isActive ? "text-primary" : "text-sidebar-foreground"
            )}>{icon}</span>
            {open && <span className="text-sm">{label}</span>}
        </>
    );

    const className = cn(
        "flex w-full items-center gap-2 font-medium transition-all duration-150",
        isActive
            ? "text-primary"
            : "text-sidebar-foreground/90 hover:text-sidebar-accent-foreground"
    );

    return (
        <SidebarMenuItem>
            <SidebarMenuButton asChild isActive={isActive} tooltip={tooltip}>
                {onClick != null ? (
                    <button type="button" onClick={onClick} className={cn(className, "w-full cursor-pointer")} style={isActive ? { color: 'var(--primary)' } : undefined} data-testid={dataTestId}>
                        {inner}
                    </button>
                ) : (
                    <Link to={path ?? "#"} className={className} style={isActive ? { color: 'var(--primary)' } : undefined} data-testid={dataTestId} aria-current={isActive ? "page" : undefined}>
                        {inner}
                    </Link>
                )}
            </SidebarMenuButton>
        </SidebarMenuItem>
    );
};

/** Props passed to the EE sidebar nav component when registered. */
export type EESidebarNavProps = {
    open: boolean;
    pathname: string;
    dropdowns: ReactNode;
};

/** Renders navigation and project, connection, and database selection. */
export const Sidebar: FC = () => {
    const { t } = useTranslation('components/sidebar');
    const schema = useAppSelector(state => state.database.schema);
    const databaseSchemaTerminology = useAppSelector(state => state.settings.databaseSchemaTerminology);
    const newUIEnabled = useAppSelector(state => state.settings.newUIEnabled);
    const isEmbedded = useAppSelector(state => state.auth.isEmbedded);
    const dispatch = useDispatch();
    const pathname = useLocation().pathname;
    const current = useAppSelector(state => state.auth.current);
    const profiles = useAppSelector(state => state.auth.profiles);
    const sslStatus = useAppSelector(state => state.auth.sslStatus);
    const {
        item,
        storageUnitLabel,
        supportsGraph,
        supportsScratchpad,
        supportsSchema,
        supportsDatabaseSwitching,
        usesDatabaseInsteadOfSchema,
    } = useSourceContract(current?.Type);
    const databaseQueryOptions = current != null && supportsDatabaseSwitching && current.Type
        ? {
            variables: {
                sourceType: current.Type,
            },
        }
        : skipToken;
    const schemaQueryVariables = useMemo(() => buildSourceSchemaQuery(item, current), [current, item]);
    const schemaQueryOptions = current != null && supportsSchema
        ? {
            variables: schemaQueryVariables,
        }
        : skipToken;
    const sslStatusQueryOptions = current != null && sslStatus === undefined
        ? {}
        : skipToken;
    const {data: availableDatabases, previousData: prevDatabases, loading: availableDatabasesLoading, refetch: getDatabases} = useQuery(SourceFieldOptionsDocument, databaseQueryOptions);
    const { data: availableSchemas, previousData: prevSchemas, loading: availableSchemasLoading, refetch: getSchemas } = useQuery(GetSchemaDocument, schemaQueryOptions);
    const hasDatabaseData = !!(availableDatabases ?? prevDatabases);
    const hasSchemaData = !!(availableSchemas ?? prevSchemas);
    const loading = (availableDatabasesLoading && !hasDatabaseData) || (availableSchemasLoading && !hasSchemaData);
    const availableSchemaNames = useMemo(() => {
        return availableSchemas?.Schema?.map(schemaObject => schemaObject.Name) ?? [];
    }, [availableSchemas?.Schema]);

    // Default schema selection: prefer Search Path, then profile database for schema-scoped
    // sources, then fall back to the first available schema.
    useEffect(() => {
        if (current == null || schema !== "" || availableSchemaNames.length === 0) return;
        const searchPath = current.Advanced?.find(a => a.Key === "Search Path")?.Value;
        const profileDatabase = (!supportsDatabaseSwitching && current.Database && availableSchemaNames.includes(current.Database))
            ? current.Database
            : undefined;
        const defaultSchema = (searchPath && availableSchemaNames.includes(searchPath))
            ? searchPath
            : (profileDatabase ?? availableSchemaNames[0] ?? "");
        dispatch(DatabaseActions.setSchema(defaultSchema));
    }, [availableSchemaNames, current, dispatch, schema, supportsDatabaseSwitching]);
    const { data: sslStatusData, refetch: refetchSslStatus } = useQuery(GetSslStatusDocument, sslStatusQueryOptions);
    useEffect(() => {
        if (sslStatusData?.SSLStatus) {
            dispatch(AuthActions.setSSLStatus(sslStatusData.SSLStatus));
        }
    }, [dispatch, sslStatusData?.SSLStatus]);
    const navigate = useNavigate();
    const [showLoginCard, setShowLoginCard] = useState(false);
    const [showProfileSwitchDialog, setShowProfileSwitchDialog] = useState(false);
    const [logoutProfileId, setLogoutProfileId] = useState<string | null>(null);
    const [platformExplainerTrigger, setPlatformExplainerTrigger] = useState<PlatformFunnelTrigger | null>(null);
    const [backupHintDismissed, setBackupHintDismissed] = useState(() => hasDismissedBackupHint());
    const { toggleSidebar, open: desktopOpen, isMobile } = useSidebar();
    // The mobile sheet always shows the expanded layout, whatever the persisted desktop collapse state is.
    const open = isMobile || desktopOpen;
    const isInitialMount = useRef(true);
    const { switchProfile, loading: switchingProfile } = useProfileSwitch({
        errorMessage: t('errorSigningIn'),
    });
    const { items: sourceTypeItems } = useSourceTypeItems();
    const { data: backendProfilesData } = useQuery(SourceProfilesDocument);

    useEffect(() => {
        const sourceProfiles = backendProfilesData?.SourceProfiles;
        if (!sourceProfiles || sourceProfiles.length === 0) return;
        const payloads = sourceProfiles
            .filter(p => p.Source !== "builtin")
            .map(p => createProfilePayloadFromSourceProfile(p));
        if (payloads.length > 0) {
            dispatch(AuthActions.syncSourceProfiles(payloads));
        }
    }, [backendProfilesData?.SourceProfiles, dispatch]);

    const hasProjects = useMemo(
        () => profiles.some(profile => resolveProfileProject(profile).hasProject),
        [profiles]
    );
    const currentProjectId = resolveProfileProject(current).id;
    const projectOptions = useMemo(() => {
        const projects = new Map<string, { value: string; label: string; icon: ReactElement }>();
        for (const profile of profiles) {
            const proj = resolveProfileProject(profile);
            if (!projects.has(proj.id)) {
                projects.set(proj.id, {
                    value: proj.id,
                    label: proj.name ?? t('localConnections'),
                    icon: <RectangleGroupIcon className="size-4" />,
                });
            }
        }
        return [...projects.values()];
    }, [profiles, t]);
    const projectProfiles = useMemo(() => {
        const filtered = hasProjects
            ? profiles.filter(profile => resolveProfileProject(profile).id === currentProjectId)
            : profiles;
        return [...filtered].sort((a, b) => {
            const orderA = DB_TYPE_SORT_ORDER[a.Type] ?? 99;
            const orderB = DB_TYPE_SORT_ORDER[b.Type] ?? 99;
            if (orderA !== orderB) return orderA - orderB;
            return a.Id.localeCompare(b.Id);
        });
    }, [currentProjectId, hasProjects, profiles]);
    const currentProjectOption = projectOptions.find(option => option.value === currentProjectId);

    const handleProjectChange = useCallback(async (value: string) => {
        if (value === currentProjectId) return;
        const instances = profiles
            .filter(profile => resolveProfileProject(profile).id === value)
            .sort((a, b) => (DB_TYPE_SORT_ORDER[a.Type] ?? 99) - (DB_TYPE_SORT_ORDER[b.Type] ?? 99));
        const instance = instances.find(profile => profile.Type === current?.Type) ?? instances[0];
        if (instance) await switchProfile(instance);
    }, [current?.Type, currentProjectId, profiles, switchProfile]);

    const profileOptions = useMemo(() => projectProfiles.map(profile => ({
            value: profile.Id,
            label: getProfileLabel(profile, findSourceTypeItem(sourceTypeItems, profile.Type)),
            icon: (
                <DatabaseIconWithBadge
                    icon={getProfileIcon(profile)}
                    sslStatus={profile.Id === current?.Id
                        ? sslStatus
                        : (profile.SSLConfigured ? { IsEnabled: true, Mode: 'configured' } : undefined)}
                    size="sm"
                />
            ),
            profile,
        })), [projectProfiles, current?.Id, sslStatus, sourceTypeItems]);

    const currentProfileOption = useMemo(() => {
        if (!current) return undefined;
        return profileOptions.find(opt => opt.value === current.Id);
    }, [current, profileOptions]);

    const handleProfileChange = useCallback(async (value: string, database?: string) => {
        const selectedProfile = profiles.find(profile => profile.Id === value);
        if (!selectedProfile) return;

        await switchProfile(selectedProfile, database);
    }, [profiles, switchProfile]);

    // Database select logic
    const [profileDatabases, setProfileDatabases] = useState<{ profileId: string; databases: string[] } | null>(() =>
        current?.Id && availableDatabases?.SourceFieldOptions
            ? { profileId: current.Id, databases: availableDatabases.SourceFieldOptions }
            : null
    );
    const fetchingDatabasesForProfileRef = useRef<string | null>(null);
    const autoSelectedDbProfileRef = useRef<string | null>(null);
    const cachedDatabases = availableDatabases?.SourceFieldOptions ?? prevDatabases?.SourceFieldOptions;
    const isCurrentProfileDatabases = Boolean(
        current?.Id && (
            profileDatabases?.profileId === current.Id ||
            (fetchingDatabasesForProfileRef.current === null && cachedDatabases != null)
        )
    );

    useEffect(() => {
        if (
            current?.Id &&
            fetchingDatabasesForProfileRef.current === null &&
            availableDatabases &&
            !availableDatabasesLoading &&
            profileDatabases?.profileId !== current.Id
        ) {
            setProfileDatabases({
                profileId: current.Id,
                databases: availableDatabases.SourceFieldOptions,
            });
        }
    }, [availableDatabases, availableDatabasesLoading, current?.Id, profileDatabases?.profileId]);

    const databaseOptions = useMemo(() => {
        const databases = profileDatabases?.profileId === current?.Id
            ? profileDatabases?.databases
            : (fetchingDatabasesForProfileRef.current === null ? cachedDatabases : undefined);
        if (!databases) return [];
        return databases.map(db => ({
            value: db,
            label: db,
        }));
    }, [cachedDatabases, current?.Id, profileDatabases]);
    
    const handleDatabaseChange = useCallback((value: string) => {
        if (value === "") {
            return;
        }
        if (!current?.Id) return;
        if (
            pathname !== InternalRoutes.Graph.path &&
            pathname !== InternalRoutes.Dashboard.StorageUnit.path &&
            pathname !== InternalRoutes.Dashboard.ExploreStorageUnit.path &&
            pathname !== InternalRoutes.DataCopy.path
        ) {
            void navigate(InternalRoutes.Dashboard.ExploreStorageUnit.path);
        }
        void handleProfileChange(current.Id, value);
    }, [current, handleProfileChange, navigate, pathname]);

    useEffect(() => {
        if (current == null) return;
        if (current.Database) {
            autoSelectedDbProfileRef.current = null;
            return;
        }
        if (
            !supportsDatabaseSwitching ||
            !isCurrentProfileDatabases ||
            availableDatabasesLoading ||
            switchingProfile ||
            databaseOptions.length === 0
        ) {
            return;
        }
        if (autoSelectedDbProfileRef.current === current.Id) {
            return;
        }
        const systemDatabases = new Set(['information_schema', 'performance_schema', 'mysql', 'sys']);
        const defaultDatabase = databaseOptions.find(
            opt => !systemDatabases.has(opt.value.toLowerCase())
        )?.value ?? databaseOptions[0]?.value;
        if (defaultDatabase) {
            autoSelectedDbProfileRef.current = current.Id;
            handleDatabaseChange(defaultDatabase);
        }
    }, [
        availableDatabasesLoading,
        current,
        databaseOptions,
        handleDatabaseChange,
        isCurrentProfileDatabases,
        supportsDatabaseSwitching,
        switchingProfile,
    ]);

    // Schema select logic
    const schemaOptions = useMemo(() => {
        const schemas = availableSchemaNames.length > 0
            ? availableSchemaNames
            : (prevSchemas?.Schema?.map(s => s.Name) ?? []);
        return schemas.map(schemaName => ({
            value: schemaName,
            label: schemaName,
        }));
    }, [availableSchemaNames, prevSchemas]);

    const handleSchemaChange = useCallback((value: string) => {
        if (value === "") {
            return;
        }
        if (
            pathname === InternalRoutes.Dashboard.StorageUnit.path ||
            pathname === InternalRoutes.Dashboard.ExploreStorageUnit.path
        ) {
            void navigate(InternalRoutes.Dashboard.ExploreStorageUnit.path, {
                replace: true,
                state: {},
            });
        } else if (
            pathname !== InternalRoutes.Graph.path &&
            pathname !== InternalRoutes.DataCopy.path &&
            pathname !== InternalRoutes.MigrationTool.path
        ) {
            void navigate(InternalRoutes.Dashboard.ExploreStorageUnit.path);
        }
        dispatch(DatabaseActions.setSchema(value));
    }, [dispatch, navigate, pathname]);

    // Sidebar routes
    const sidebarRoutes = useMemo(() => {
        if (!current) return [];
        const routes: { title: string; icon: React.ReactNode; path: string }[] = [
            {
                title: storageUnitLabel,
                icon: newUIEnabled ? <TableCellsIcon className="w-4 h-4" /> : <WhoDBChatIcon name="table" className="size-4" />,
                path: InternalRoutes.Dashboard.ExploreStorageUnit.path,
            },
        ];
        if (supportsGraph) {
            routes.push({
                title: t('graph'),
                icon: newUIEnabled ? <RectangleGroupIcon className="w-4 h-4" /> : <WhoDBChatIcon name="relation" className="size-4" />,
                path: InternalRoutes.Graph.path,
            });
        }
        if (supportsScratchpad) {
            routes.push({
                title: t('scratchpad'),
                icon: newUIEnabled ? <CommandLineIcon className="w-4 h-4" /> : <WhoDBChatIcon name="code" className="size-4" />,
                path: InternalRoutes.RawExecute.path,
            });
        }
        routes.push({
            title: t('dataCopy'),
            icon: newUIEnabled ? <DocumentDuplicateIcon className="w-4 h-4" /> : <WhoDBChatIcon name="copy" className="size-4" />,
            path: InternalRoutes.DataCopy.path,
        });
        routes.push({
            title: t('migrationTool'),
            icon: newUIEnabled ? <CogIcon className="w-4 h-4" /> : <WhoDBChatIcon name="code" className="size-4" />,
            path: InternalRoutes.MigrationTool.path,
        });
        return routes;
    }, [current, newUIEnabled, storageUnitLabel, supportsGraph, supportsScratchpad, t]);

    // Profile switch dialog handlers
    const handleDialogProfileSwitch = useCallback(async (profile: LocalLoginProfile) => {
        setShowProfileSwitchDialog(false);
        await switchProfile(profile);
        if (logoutProfileId) {
            dispatch(AuthActions.remove({ id: logoutProfileId }));
            setLogoutProfileId(null);
        }
    }, [switchProfile, logoutProfileId, dispatch]);

    const handleDialogAddProfile = useCallback(() => {
        setShowProfileSwitchDialog(false);
        // logoutProfileId stays set so onLoginSuccess can clean it up
        setShowLoginCard(true);
    }, []);

    const handleLoginSuccess = useCallback(() => {
        setShowLoginCard(false);
        if (logoutProfileId) {
            dispatch(AuthActions.remove({ id: logoutProfileId }));
            setLogoutProfileId(null);
        }
    }, [logoutProfileId, dispatch]);

    const handleProfileSwitchDialogChange = useCallback((open: boolean) => {
        if (!open) {
            setLogoutProfileId(null);
        }
        setShowProfileSwitchDialog(open);
    }, []);

    // Add profile logic
    const handleAddProfile = useCallback(() => {
        // Small delay to allow dropdown to close before opening sheet
        setTimeout(() => {
            setShowLoginCard(true);
        }, 100);
    }, []);

    // Load EE sidebar nav component if registered
    const EESidebarNav = getComponent('ee-sidebar-nav') as LazyExoticComponent<FC<EESidebarNavProps>> | undefined;

    // Footer entry that opens the WhoDB Platform explainer.
    const platformFooterItem = featureFlags.platformFunnel && !isEmbedded ? (
        <NavItem
            icon={<CloudIcon className="w-4 h-4" />}
            label={t('platformTitle')}
            pathname={pathname}
            open={open}
            tooltip={t('platformTitle')}
            onClick={() => { setPlatformExplainerTrigger("sidebar"); }}
            data-testid="sidebar-platform-funnel"
        />
    ) : null;

    // One-time hint about team access to saved connections. Shown
    // once a user has accumulated a few profiles; dismissing it is permanent.
    const backupNudge = featureFlags.platformFunnel && !isEmbedded && !backupHintDismissed && profiles.length >= 3 && open ? (
        <div
            className="relative mx-2 my-2 rounded-lg border border-neutral-200 dark:border-neutral-800 p-3 pr-8 flex flex-col gap-2"
            data-testid="sidebar-backup-nudge"
        >
            <button
                type="button"
                aria-label={t('platformBackupNudgeDismiss')}
                onClick={() => {
                    dismissBackupHint();
                    setBackupHintDismissed(true);
                    trackPlatformFunnel(ANALYTICS_EVENTS.PLATFORM_NUDGE_DISMISSED, "backup_nudge", {
                        connection_count_bucket: countBucket(profiles.length),
                    });
                }}
                className="absolute right-2 top-2 text-neutral-400 hover:text-neutral-600 dark:hover:text-neutral-300"
                data-testid="sidebar-backup-nudge-dismiss"
            >
                <XMarkIcon className="w-4 h-4" />
            </button>
            <p className="text-xs text-neutral-600 dark:text-neutral-400">{t('platformBackupNudgeText')}</p>
            <Button
                variant="outline"
                size="sm"
                className="self-start"
                onClick={() => { setPlatformExplainerTrigger("backup_nudge"); }}
            >
                {t('platformBackupNudgeAction')}
            </Button>
        </div>
    ) : null;

    // Compute the label for the database dropdown based on the database type and user terminology preference
    const databaseDropdownLabel = useMemo(() => {
        // For databases where database=schema (MySQL, MariaDB, ClickHouse, MongoDB, Redis), allow user to choose terminology
        if (usesDatabaseInsteadOfSchema) {
            return databaseSchemaTerminology === 'schema' ? t('schema') : t('database');
        }
        // For all other databases, use "Database"
        return t('database');
    }, [databaseSchemaTerminology, t, usesDatabaseInsteadOfSchema]);

    // Refetch databases, schemas, and SSL status when the connection context changes
    // (profile switch or database switch within the same profile)
    const currentId = current?.Id;
    const currentDatabase = current?.Database;
    const currentType = current?.Type;
    useEffect(() => {
        if (isInitialMount.current) {
            isInitialMount.current = false;
            return;
        }
        if (!currentId) return;
        if (supportsDatabaseSwitching && currentType) {
            const targetProfileId = currentId;
            fetchingDatabasesForProfileRef.current = targetProfileId;
            setProfileDatabases(prev => (prev?.profileId === targetProfileId ? prev : null));
            void getDatabases({ sourceType: currentType }).then(({ data }) => {
                if (fetchingDatabasesForProfileRef.current === targetProfileId) {
                    fetchingDatabasesForProfileRef.current = null;
                    if (data) {
                        setProfileDatabases({
                            profileId: targetProfileId,
                            databases: data.SourceFieldOptions,
                        });
                    }
                }
            }).catch(() => {
                if (fetchingDatabasesForProfileRef.current === targetProfileId) {
                    fetchingDatabasesForProfileRef.current = null;
                }
            });
        }
        if (supportsSchema) {
            void getSchemas();
        }
        void refetchSslStatus().then(({ data }) => {
            if (data?.SSLStatus) {
                dispatch(AuthActions.setSSLStatus(data.SSLStatus));
            }
        });
    }, [currentId, currentDatabase, currentType, dispatch, getDatabases, getSchemas, refetchSslStatus, supportsDatabaseSwitching, supportsSchema]);

    // Listen for menu event to open add profile form
    useEffect(() => {
        const handleOpenAddProfile = () => {
            // Open the sidebar if it's closed
            if (!open) {
                toggleSidebar();
            }
            // Open the add profile sheet
            setShowLoginCard(true);
        };

        const handleToggleSidebar = () => {
            toggleSidebar();
        };

        window.addEventListener('menu:open-add-profile', handleOpenAddProfile);
        window.addEventListener('menu:toggle-sidebar', handleToggleSidebar);
        return () => {
            window.removeEventListener('menu:open-add-profile', handleOpenAddProfile);
            window.removeEventListener('menu:toggle-sidebar', handleToggleSidebar);
        };
    }, [open, toggleSidebar]);

    const profileLabel = hasProjects ? t('databaseInstance') : t('profile');
    const connectionSelectors = (
        <>
            {hasProjects && (
                <div className="flex flex-col gap-sm w-full ce-sidebar-profile-wrap">
                    {newUIEnabled && <h2 className={cn("text-sm", { hidden: !open })}>{t('project')}</h2>}
                    {!newUIEnabled && open && (
                        <div className="ce-sidebar-profile-display" aria-hidden="true">
                            <span className="ce-sidebar-profile-icon"><RectangleGroupIcon className="size-4" /></span>
                            <span className="ce-sidebar-profile-copy">
                                <strong>{currentProjectOption?.label}</strong>
                                <small>{t('instanceCount', { count: projectProfiles.length })}</small>
                            </span>
                            <span className="ce-sidebar-profile-chevron"><WhoDBChatIcon name="chevron-up" /><WhoDBChatIcon name="chevron-down" /></span>
                        </div>
                    )}
                    <SearchSelect
                        label={t('project')}
                        options={projectOptions}
                        value={currentProjectId}
                        onChange={value => { void handleProjectChange(value); }}
                        disabled={switchingProfile}
                        placeholder={t('selectProject')}
                        searchPlaceholder={t('searchProject')}
                        onlyIcon={!open}
                        side={open ? "bottom" : "right"} align="start"
                        buttonClassName={cn(ph.mask, !newUIEnabled && open && 'ce-sidebar-profile-button')}
                        buttonProps={{ "data-testid": "sidebar-project", "aria-label": t('project'), "data-collapsed": !open }}
                    />
                </div>
            )}
            <div className={cn("flex flex-col gap-sm w-full", !hasProjects && 'ce-sidebar-profile-wrap')}>
                {(!newUIEnabled || hasProjects) && <h2 className={cn("text-sm", { hidden: !open })}>{profileLabel}</h2>}
                {!hasProjects && !newUIEnabled && open && current && (
                    <div className="ce-sidebar-profile-display" aria-hidden="true">
                        <span className="ce-sidebar-profile-icon"><WhoDBChatIcon name="source" /></span>
                        <span className="ce-sidebar-profile-copy">
                            <strong>{current.Database === 'whodb-sample' ? t('sampleSQLiteName') : (current.DisplayName ?? getProfileLabel(current, findSourceTypeItem(sourceTypeItems, current.Type)))}</strong>
                            <small>{current.Database === 'whodb-sample' ? t('sampleSQLiteFilename') : (current.Database ? current.Database : current.Hostname)}</small>
                        </span>
                        <span className="ce-sidebar-profile-chevron"><WhoDBChatIcon name="chevron-up" /><WhoDBChatIcon name="chevron-down" /></span>
                    </div>
                )}
                <SearchSelect
                    label={profileLabel}
                    options={profileOptions}
                    value={currentProfileOption?.value}
                    onChange={value => { void handleProfileChange(value); }}
                    disabled={switchingProfile}
                    placeholder={hasProjects ? t('selectInstance') : t('selectProfile')}
                    searchPlaceholder={hasProjects ? t('searchInstance') : t('searchProfile')}
                    onlyIcon={!open}
                    extraOptions={!isEmbedded ? (
                        <CommandItem key="__add__" value="__add__" onSelect={handleAddProfile}>
                            <span className="flex items-center gap-sm text-green-500">
                                <PlusCircleIcon className="w-4 h-4 stroke-green-500" />
                                {t('addAnotherProfile')}
                            </span>
                        </CommandItem>
                    ) : undefined}
                    side={open ? "bottom" : "right"} align="start"
                    buttonClassName={cn(ph.mask, !hasProjects && !newUIEnabled && open && 'ce-sidebar-profile-button')}
                    buttonProps={{ "data-testid": "sidebar-profile", "data-collapsed": !open, "aria-label": profileLabel }}
                />
            </div>
        </>
    );

    return (
        <nav aria-label={t('mainNavigation')}>
            <SidebarComponent
                variant="sidebar"
                collapsible="icon"
                className={cn('z-[50]', !newUIEnabled && 'ce-tables-sidebar', !newUIEnabled && pathname.includes(InternalRoutes.Dashboard.ExploreStorageUnit.path) && 'ce-explore-sidebar')}
            >
                <SidebarHeader className={cn({ "ml-4": open })}>
                    <div className="flex items-center gap-sm justify-between">
                        <div className={cn("flex items-center gap-sm mt-2", { "hidden": !open })}>
                            {extensions.Logo ?? <img src={logoImage} alt="clidey logo" className={cn("w-auto", newUIEnabled ? "h-6" : "h-8")} />}
                            {open && <span className={cn("font-bold", newUIEnabled ? "text-lg" : "text-3xl")} data-testid="app-name">{getAppName()}</span>}
                        </div>
                        {!newUIEnabled && open ? (
                            <button type="button" className="ce-sidebar-toggle" onClick={toggleSidebar} aria-label={t('collapseNavigation')}>
                                <WhoDBChatIcon name="sidebar" />
                            </button>
                        ) : (
                            <SidebarTrigger className="px-0" />
                        )}
                    </div>
                </SidebarHeader>
                <SidebarContent className={cn("mt-4 mb-4 overflow-y-auto", { "mx-4": open })}>
                    {newUIEnabled ? (
                    <SidebarGroup className="grow">
                        <SidebarMenu className="gap-0 grow">
                            {EESidebarNav ? (
                                <Suspense fallback={null}>
                                    <EESidebarNav open={open} pathname={pathname} dropdowns={
                                        <>
                                            {connectionSelectors}
                                            {supportsDatabaseSwitching && (
                                                <SearchSelect
                                                    label={databaseDropdownLabel}
                                                    options={databaseOptions}
                                                    value={current?.Database}
                                                    onChange={handleDatabaseChange}
                                                    disabled={availableDatabasesLoading}
                                                    placeholder={databaseSchemaTerminology === 'schema' && usesDatabaseInsteadOfSchema ? t('selectSchema') : t('selectDatabase')}
                                                    searchPlaceholder={databaseSchemaTerminology === 'schema' && usesDatabaseInsteadOfSchema ? t('searchSchema') : t('searchDatabase')}
                                                    side="left" align="start"
                                                    buttonClassName={ph.mask}
                                                    buttonProps={{ "data-testid": "sidebar-database", "aria-label": databaseDropdownLabel }}
                                                />
                                            )}
                                            {supportsSchema && !pathname.includes(InternalRoutes.RawExecute.path) && (
                                                <SearchSelect
                                                    label={t('schema')}
                                                    options={schemaOptions}
                                                    value={schema}
                                                    onChange={handleSchemaChange}
                                                    disabled={availableSchemasLoading}
                                                    placeholder={t('selectSchema')}
                                                    searchPlaceholder={t('searchSchema')}
                                                    side="left" align="start"
                                                    buttonClassName={ph.mask}
                                                    buttonProps={{ "data-testid": "sidebar-schema" }}
                                                />
                                            )}
                                        </>
                                    } />
                                </Suspense>
                            ) : (
                                <>
                                    {open && (
                                        <div className="flex flex-col gap-sm mb-4">
                                            {connectionSelectors}
                                            {supportsDatabaseSwitching && (
                                                <SearchSelect
                                                    label={databaseDropdownLabel}
                                                    options={databaseOptions}
                                                    value={current?.Database}
                                                    onChange={handleDatabaseChange}
                                                    disabled={availableDatabasesLoading}
                                                    placeholder={databaseSchemaTerminology === 'schema' && usesDatabaseInsteadOfSchema ? t('selectSchema') : t('selectDatabase')}
                                                    searchPlaceholder={databaseSchemaTerminology === 'schema' && usesDatabaseInsteadOfSchema ? t('searchSchema') : t('searchDatabase')}
                                                    side="left" align="start"
                                                    buttonClassName={ph.mask}
                                                    buttonProps={{ "data-testid": "sidebar-database", "aria-label": databaseDropdownLabel }}
                                                />
                                            )}
                                            {supportsSchema && !pathname.includes(InternalRoutes.RawExecute.path) && (
                                                <SearchSelect
                                                    label={t('schema')}
                                                    options={schemaOptions}
                                                    value={schema}
                                                    onChange={handleSchemaChange}
                                                    disabled={availableSchemasLoading}
                                                    placeholder={t('selectSchema')}
                                                    searchPlaceholder={t('searchSchema')}
                                                    side="left" align="start"
                                                    buttonClassName={ph.mask}
                                                    buttonProps={{ "data-testid": "sidebar-schema" }}
                                                />
                                            )}
                                        </div>
                                    )}
                                    {sidebarRoutes.map(route => (
                                        <NavItem
                                            key={route.path}
                                            icon={route.icon}
                                            label={route.title}
                                            path={route.path}
                                            pathname={pathname}
                                            open={open}
                                            tooltip={route.title}
                                        />
                                    ))}

                                    <SidebarSeparator className={cn("my-4", { "mx-0": !open })} />

                                    {backupNudge}
                                    {platformFooterItem}
                                    {featureFlags.settingsPage && InternalRoutes.Settings && (
                                        <NavItem icon={<CogIcon className="w-4 h-4" />} label={t('settings')} path={InternalRoutes.Settings.path} pathname={pathname} open={open} tooltip={t('settings')} />
                                    )}
                                </>
                            )}

                            {!EESidebarNav && <div className="grow" />}
                        </SidebarMenu>
                    </SidebarGroup>
                    ) : loading ? (
                        <div className="flex justify-center items-center h-full">
                            <Loading size="lg" />
                        </div>
                    ) : (
                        <SidebarGroup className="grow">
                            <div className="flex flex-col gap-lg">
                                {connectionSelectors}
                                {supportsDatabaseSwitching && (
                                    <div className={cn("flex flex-col gap-sm w-full", {
                                        "opacity-0 pointer-events-none": !open,
                                    })}>
                                        <h2 className="text-sm" data-testid="sidebar-database-label">{databaseDropdownLabel}</h2>
                                        <SearchSelect
                                            label={databaseDropdownLabel}
                                            options={databaseOptions}
                                            value={current?.Database}
                                            onChange={handleDatabaseChange}
                                            disabled={availableDatabasesLoading}
                                            placeholder={databaseSchemaTerminology === 'schema' && usesDatabaseInsteadOfSchema ? t('selectSchema') : t('selectDatabase')}
                                            searchPlaceholder={databaseSchemaTerminology === 'schema' && usesDatabaseInsteadOfSchema ? t('searchSchema') : t('searchDatabase')}
                                            side="left" align="start"
                                            buttonClassName={ph.mask}
                                            buttonProps={{
                                                "data-testid": "sidebar-database",
                                                "aria-label": databaseDropdownLabel,
                                            }}
                                        />
                                    </div>
                                )}
                                {supportsSchema && (
                                    <div className={cn("flex flex-col gap-sm w-full", {
                                        "opacity-0 pointer-events-none": !open || pathname.includes(InternalRoutes.RawExecute.path),
                                    })}>
                                        <h2 className="text-sm">{t('schema')}</h2>
                                        <SearchSelect
                                            label={t('schema')}
                                            options={schemaOptions}
                                            value={schema}
                                            onChange={handleSchemaChange}
                                            disabled={availableSchemasLoading}
                                            placeholder={t('selectSchema')}
                                            searchPlaceholder={t('searchSchema')}
                                            side="left" align="start"
                                            buttonClassName={ph.mask}
                                            buttonProps={{
                                                "data-testid": "sidebar-schema",
                                            }}
                                        />
                                    </div>
                                )}
                            </div>

                            <SidebarMenu className="grow mt-8 gap-4">
                                {sidebarRoutes.map((route) => {
                                    const isRouteActive = pathname === route.path || (
                                        route.path === InternalRoutes.Dashboard.ExploreStorageUnit.path &&
                                        pathname === InternalRoutes.Dashboard.StorageUnit.path
                                    );
                                    return (
                                    <SidebarMenuItem key={route.title}>
                                        <SidebarMenuButton asChild tooltip={open ? undefined : route.title} isActive={isRouteActive}>
                                            <Link
                                                to={route.path}
                                                aria-label={open ? undefined : route.title}
                                                className={cn("flex w-full items-center gap-2", {
                                                    "font-bold": isRouteActive,
                                                })}
                                                aria-current={isRouteActive ? 'page' : undefined}
                                            >
                                                {route.icon}
                                                {open && <span>{route.title}</span>}
                                            </Link>
                                        </SidebarMenuButton>
                                    </SidebarMenuItem>
                                    );
                                })}

                                <SidebarMenuItem className="grow" aria-hidden="true" />
                                <SidebarSeparator className={cn("my-2", {
                                    "mx-0": !open,
                                })} />

                                {featureFlags.platformFunnel && !isEmbedded && (
                                    <SidebarMenuItem>
                                        <SidebarMenuButton asChild tooltip={t('platformTitle')}>
                                            <div
                                                onClick={() => { setPlatformExplainerTrigger("sidebar"); }}
                                                className="flex items-center gap-2 cursor-pointer"
                                                data-testid="sidebar-platform-funnel"
                                            >
                                                <CloudIcon className="w-4 h-4" />
                                                {open && <span>{t('platformTitle')}</span>}
                                            </div>
                                        </SidebarMenuButton>
                                    </SidebarMenuItem>
                                )}
                                {featureFlags.settingsPage && InternalRoutes.Settings && (
                                    <SidebarMenuItem>
                                        <SidebarMenuButton asChild tooltip={t('settings')}>
                                            <Link
                                                to={InternalRoutes.Settings.path}
                                                aria-label={open ? undefined : t('settings')}
                                                className={cn("flex items-center gap-2", {
                                                    "font-bold": pathname === InternalRoutes.Settings.path,
                                                })}
                                            >
                                                <WhoDBChatIcon name="settings" />
                                                {open && <span>{t('settings')}</span>}
                                            </Link>
                                        </SidebarMenuButton>
                                    </SidebarMenuItem>
                                )}
                            </SidebarMenu>
                        </SidebarGroup>
                    )}
                </SidebarContent>
            </SidebarComponent>
            <Sheet open={showLoginCard} onOpenChange={setShowLoginCard}>
                <SheetContent side="right" className="w-full sm:max-w-md p-2">
                    <VisuallyHidden>
                        <SheetTitle>{t('databaseLogin')}</SheetTitle>
                    </VisuallyHidden>
                    <LoginForm
                        advancedDirection="vertical"
                        defaultProjectId={currentProjectId !== "__local__" ? currentProjectId : undefined}
                        onLoginSuccess={handleLoginSuccess}
                    />
                </SheetContent>
            </Sheet>
            <PlatformExplainerDialog
                open={platformExplainerTrigger != null}
                onOpenChange={(dialogOpen) => { if (!dialogOpen) setPlatformExplainerTrigger(null); }}
                trigger={platformExplainerTrigger ?? "sidebar"}
            />
            <Dialog open={showProfileSwitchDialog} onOpenChange={handleProfileSwitchDialogChange}>
                <DialogContent className="max-w-sm" onInteractOutside={(e) => { e.preventDefault(); }} onEscapeKeyDown={(e) => { e.preventDefault(); }}>
                    <DialogHeader>
                        <DialogTitle>{t('switchProfile')}</DialogTitle>
                        <DialogDescription>{t('switchProfileDescription')}</DialogDescription>
                    </DialogHeader>
                    <div className="flex flex-col gap-1 mt-2">
                        {profiles.filter(p => p.Id !== logoutProfileId).map(profile => {
                            const sourceTypeItem = findSourceTypeItem(sourceTypeItems, profile.Type);
                            return (
                                <button
                                    key={profile.Id}
                                    className="flex items-center gap-3 p-3 rounded-lg hover:bg-accent transition-colors text-left w-full"
                                    onClick={() => { void handleDialogProfileSwitch(profile); }}
                                >
                                    <DatabaseIconWithBadge
                                        icon={getProfileIcon(profile)}
                                        size="sm"
                                    />
                                    <span className={cn("text-sm font-medium truncate", ph.mask)}>{getProfileLabel(profile, sourceTypeItem)}</span>
                                </button>
                            );
                        })}
                        <button
                            className="flex items-center gap-3 p-3 rounded-lg hover:bg-accent transition-colors text-left w-full text-green-500"
                            onClick={handleDialogAddProfile}
                        >
                            <PlusCircleIcon className="w-4 h-4 stroke-green-500" />
                            <span className="text-sm font-medium">{t('addAnotherProfile')}</span>
                        </button>
                    </div>
                </DialogContent>
            </Dialog>
        </nav>
    );
};
