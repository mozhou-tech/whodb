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

import { skipToken, useApolloClient, useLazyQuery, useMutation, useQuery } from "@apollo/client/react";
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    Badge,
    Button,
    cn,
    ContextMenu,
    ContextMenuContent,
    ContextMenuItem,
    ContextMenuSeparator,
    ContextMenuTrigger,
    SearchInput,
    Sheet,
    SheetContent,
    SheetTitle,
    Sidebar as SidebarComponent,
    SidebarContent,
    SidebarGroup,
    StackList,
    StackListItem,
    toast,
    IconClipboard as ClipboardDocumentIcon,
    IconCopy as DocumentDuplicateIcon,
    IconEye as EyeIcon,
    IconRefresh as ArrowPathRoundedSquareIcon,
    IconInfo as InformationCircleIcon,
    IconPlusCircle as PlusCircleIcon,
    IconTable as TableCellsIcon,
    IconTrash as TrashIcon,
} from "@clidey/ux";
import type { GetColumnsBatchQuery, GetStorageUnitsQuery, SourceObjectRefInput} from "@graphql";
import {
    ClearSourceObjectDataDocument,
    DataShape,
    DropSourceObjectDocument,
    GetColumnsBatchDocument,
    GetStorageUnitRowsDocument,
    GetStorageUnitsDocument,
    SourceAction,
    SourceSchemaFidelity,
} from "@graphql";
import {WhoDBChatIcon} from "./whodb-chat-icon";
import type {FC} from "react";
import { useCallback, useEffect, useMemo, useState} from "react";
import {useLocation, useNavigate, useSearchParams} from "react-router-dom";
import {InternalRoutes} from "../config/routes";
import {findSourceObjectType, type SourceTypeItem} from "../config/source-types";
import {useSourceContract} from "../hooks/useSourceContract";
import {CreateSourceObjectCard} from "../pages/storage-unit/create-source-object-card";
import {useAppSelector} from "../store/hooks";
import {Loading} from "./loading";
import {useTranslation} from "@/hooks/use-translation";
import {formatAttributeKey, formatAttributeValue} from "@/utils/functions";
import {buildSourceParentRef} from "@/utils/source-refs";
import {ph} from "@/utils/privacy";

type SourceBrowserObject = GetStorageUnitsQuery['StorageUnit'][number];
type SourceColumn = GetColumnsBatchQuery['ColumnsBatch'][number]['Columns'][number];

function isTabularSourceObject(
    item: SourceTypeItem | undefined,
    unit: SourceBrowserObject
): boolean {
    const dataShape = findSourceObjectType(item, unit.Kind)?.DataShape;
    return dataShape === DataShape.Tabular || dataShape === DataShape.Document;
}

function sourceObjectRefKey(ref: { Kind: unknown; Locator?: string | null; Path?: readonly string[] | null }): string {
    return `${String(ref.Kind)}:${ref.Locator ?? ""}:${ref.Path?.join("/") ?? ""}`;
}

type SchemaViewerProps = {
    parentRef?: SourceObjectRefInput;
    selectedName?: string;
    trail?: SourceBrowserObject[];
};

type SchemaViewerUnitProps = {
    unit: SourceBrowserObject;
    selected: boolean;
    onSelect: (name: string) => void;
    onDescribe: (unit: SourceBrowserObject) => void;
    onCopyName: (name: string) => void;
    onDataCopy: () => void;
    onTruncate: (unit: SourceBrowserObject) => void;
    onDrop: (unit: SourceBrowserObject) => void;
    labels: {
        viewData: string;
        describeTable: string;
        copyTableName: string;
        copyTableData: string;
        truncateTable: string;
        dropTable: string;
    };
};

const SchemaViewerUnit: FC<SchemaViewerUnitProps> = ({
    unit,
    selected,
    onSelect,
    onDescribe,
    onCopyName,
    onDataCopy,
    onTruncate,
    onDrop,
    labels,
}) => {
    const metadataCount = unit.Attributes.find(attribute => attribute.Key === 'Count')?.Value;
    const knownCount = metadataCount != null && metadataCount !== 'unknown' ? metadataCount : undefined;
    const {data} = useQuery(GetStorageUnitRowsDocument, knownCount == null && unit.Actions.includes(SourceAction.ViewRows)
        ? {variables: {ref: unit.Ref, pageSize: 1, pageOffset: 0}}
        : skipToken);
    const count = knownCount ?? data?.Row.TotalCount;
    const canTruncate = unit.Actions.includes(SourceAction.DeleteData);

    return (
        <ContextMenu>
            <ContextMenuTrigger className="contents">
                <button
                    type="button"
                    className={selected ? 'is-selected' : ''}
                    onClick={() => { onSelect(unit.Name); }}
                    aria-current={selected ? 'page' : undefined}
                >
                    <WhoDBChatIcon name="table" />
                    <span className="truncate">{unit.Name}</span>
                    {count != null && <span className="ce-schema-row-count shrink-0">{count}</span>}
                </button>
            </ContextMenuTrigger>
            <ContextMenuContent className="w-52">
                <ContextMenuItem onSelect={() => { onSelect(unit.Name); }}>
                    <EyeIcon className="w-4 h-4" />
                    {labels.viewData}
                </ContextMenuItem>
                <ContextMenuItem onSelect={() => { onDescribe(unit); }}>
                    <InformationCircleIcon className="w-4 h-4" />
                    {labels.describeTable}
                </ContextMenuItem>
                <ContextMenuItem onSelect={() => { onCopyName(unit.Name); }}>
                    <ClipboardDocumentIcon className="w-4 h-4" />
                    {labels.copyTableName}
                </ContextMenuItem>
                <ContextMenuItem onSelect={() => { onDataCopy(); }}>
                    <DocumentDuplicateIcon className="w-4 h-4" />
                    {labels.copyTableData}
                </ContextMenuItem>
                <ContextMenuSeparator />
                {canTruncate && (
                    <ContextMenuItem
                        variant="destructive"
                        onSelect={() => { onTruncate(unit); }}
                        className="text-amber-500 [&>svg]:text-amber-500"
                    >
                        <TrashIcon className="w-4 h-4" />
                        {labels.truncateTable}
                    </ContextMenuItem>
                )}
                <ContextMenuItem
                    variant="destructive"
                    onSelect={() => { onDrop(unit); }}
                    className="text-red-500 [&>svg]:text-red-500"
                >
                    <TrashIcon className="w-4 h-4" />
                    {labels.dropTable}
                </ContextMenuItem>
            </ContextMenuContent>
        </ContextMenu>
    );
};

export const SchemaViewer: FC<SchemaViewerProps> = ({ parentRef: explicitParentRef, selectedName, trail = [] }) => {
    const { t } = useTranslation('components/schema-viewer');
    const { t: tStorageUnit } = useTranslation('pages/storage-unit');
    const { t: tCommon } = useTranslation('common');
    const apolloClient = useApolloClient();
    const current = useAppSelector(state => state.auth.current);
    const currentType = current?.Type;
    const currentDatabase = current?.Database;
    const selectedSchema = useAppSelector(state => state.database.schema);
    const {
        item,
        schemaFidelity,
        singularStorageUnitLabel,
        supportsSchema,
    } = useSourceContract(currentType);
    const navigate = useNavigate();
    const state = useLocation().state as { unit?: SourceBrowserObject } | undefined;
    const [searchParams, setSearchParams] = useSearchParams();

    // Search & table-level action state
    const [search, setSearch] = useState("");
    const [createOpen, setCreateOpen] = useState(searchParams.get("create") === "true");
    const [createError, setCreateError] = useState<string>();
    const [describeOpen, setDescribeOpen] = useState(false);
    const [describeTargetUnit, setDescribeTargetUnit] = useState<SourceBrowserObject | null>(null);
    const [truncateTargetUnit, setTruncateTargetUnit] = useState<SourceBrowserObject | null>(null);
    const [dropTargetUnit, setDropTargetUnit] = useState<SourceBrowserObject | null>(null);
    const [storageUnitColumnsByName, setStorageUnitColumnsByName] = useState<Record<string, SourceColumn[]>>({});
    const [describeColumnsLoading, setDescribeColumnsLoading] = useState(false);
    const [fetchColumnsBatch] = useLazyQuery(GetColumnsBatchDocument);
    const [clearSourceObjectData, { loading: truncating }] = useMutation(ClearSourceObjectDataDocument);
    const [dropSourceObject, { loading: dropping }] = useMutation(DropSourceObjectDocument);

    const parentRef = explicitParentRef ?? buildSourceParentRef(item, current, selectedSchema);
    const parentRefKey = parentRef ? sourceObjectRefKey(parentRef) : "";
    const canCreateObjects = useMemo(() => {
        const parentKind = parentRef?.Kind;
        if (parentKind) {
            return findSourceObjectType(item, parentKind)?.Actions.includes(SourceAction.CreateChild) ?? false;
        }
        return item?.contract?.RootActions?.includes(SourceAction.CreateChild) ?? false;
    }, [item, parentRef?.Kind]);

    const storageUnitsQueryOptions = current && (explicitParentRef != null || !supportsSchema || selectedSchema !== "")
        ? {
            variables: {
                parent: parentRef,
            },
        }
        : skipToken;

    // Query for storage units (tables, views, etc.)
    const {data, loading, refetch} = useQuery(GetStorageUnitsDocument, storageUnitsQueryOptions);

    // Refetch when the connection context changes (profile switch or database switch)
    const currentProfileId = current?.Id;
    useEffect(() => {
        if (currentProfileId) {
            void refetch();
        }
    }, [currentProfileId, currentDatabase, refetch]);

    useEffect(() => {
        setStorageUnitColumnsByName({});
    }, [currentProfileId, currentDatabase, parentRefKey]);

    useEffect(() => {
        if (searchParams.get("create") === "true") {
            setCreateOpen(true);
            const nextParams = new URLSearchParams(searchParams);
            nextParams.delete("create");
            setSearchParams(nextParams, { replace: true });
        }
    }, [searchParams, setSearchParams]);

    const storageUnits = useMemo(() => {
        return (data?.StorageUnit ?? []) as SourceBrowserObject[];
    }, [data?.StorageUnit]);

    const activeUnitName = selectedName ?? state?.unit?.Name;
    const activeUnit = useMemo(() => {
        if (!activeUnitName) {
            return state?.unit ?? storageUnits[0];
        }
        return storageUnits.find(u => u.Name === activeUnitName) ?? state?.unit;
    }, [activeUnitName, state?.unit, storageUnits]);

    const referenceStorageUnits = useMemo(() => {
        return storageUnits.filter(unit => isTabularSourceObject(item, unit));
    }, [item, storageUnits]);

    const referenceColumnsByName = useMemo(() => {
        return Object.fromEntries(
            Object.entries(storageUnitColumnsByName).map(([unitName, columns]) => [
                unitName,
                columns.map(column => column.Name),
            ])
        );
    }, [storageUnitColumnsByName]);

    // Load columns for FK reference picker when Create Table sheet opens
    useEffect(() => {
        if (!createOpen || referenceStorageUnits.length === 0) {
            return;
        }
        const missingUnits = referenceStorageUnits.filter(unit => storageUnitColumnsByName[unit.Name] == null);
        if (missingUnits.length === 0) {
            return;
        }
        const unitNameByRef = new Map(missingUnits.map(unit => [sourceObjectRefKey(unit.Ref), unit.Name]));
        fetchColumnsBatch({
            variables: { refs: missingUnits.map(unit => unit.Ref) },
        }).then(result => {
            setStorageUnitColumnsByName(prev => {
                const next = { ...prev };
                for (const unit of missingUnits) {
                    next[unit.Name] = [];
                }
                for (const batch of result.data?.ColumnsBatch ?? []) {
                    const unitName = unitNameByRef.get(sourceObjectRefKey(batch.StorageUnit));
                    if (unitName) {
                        next[unitName] = batch.Columns;
                    }
                }
                return next;
            });
        }).catch(() => {
            setStorageUnitColumnsByName(prev => {
                const next = { ...prev };
                for (const unit of missingUnits) {
                    next[unit.Name] = [];
                }
                return next;
            });
        });
    }, [createOpen, fetchColumnsBatch, referenceStorageUnits, storageUnitColumnsByName]);

    const describeUnit = describeTargetUnit ?? activeUnit;

    // Load columns for Describe (structure details) sheet when opened
    useEffect(() => {
        if (!describeOpen || !describeUnit) {
            return;
        }
        if (storageUnitColumnsByName[describeUnit.Name] != null) {
            return;
        }
        if (!isTabularSourceObject(item, describeUnit)) {
            setStorageUnitColumnsByName(prev => ({ ...prev, [describeUnit.Name]: [] }));
            return;
        }
        const unitName = describeUnit.Name;
        setDescribeColumnsLoading(true);
        fetchColumnsBatch({
            variables: { refs: [describeUnit.Ref] },
        }).then(result => {
            const loadedColumns = result.data?.ColumnsBatch?.[0]?.Columns ?? [];
            setStorageUnitColumnsByName(prev => ({ ...prev, [unitName]: loadedColumns }));
        }).catch(() => {
            setStorageUnitColumnsByName(prev => ({ ...prev, [unitName]: [] }));
        }).finally(() => {
            setDescribeColumnsLoading(false);
        });
    }, [describeOpen, describeUnit, fetchColumnsBatch, item, storageUnitColumnsByName]);

    // Auto-select the first available storage unit when entering ExploreStorageUnit
    // directly or after switching database / schema / profile.
    useEffect(() => {
        if (loading || storageUnits.length === 0) {
            return;
        }
        const currentStateUnit = state?.unit;
        const activePathKey = currentStateUnit?.Ref?.Path?.join("/") ?? "";
        const hasValidActiveUnit = currentStateUnit != null && storageUnits.some(
            u => u.Name === currentStateUnit.Name && (u.Ref.Path?.join("/") ?? "") === activePathKey
        );
        if (hasValidActiveUnit) {
            return;
        }
        const preferredName = selectedName ?? currentStateUnit?.Name;
        const nextUnit =
            (preferredName ? storageUnits.find(u => u.Name === preferredName) : undefined) ??
            storageUnits.find(u => !(u.Actions.includes(SourceAction.Browse) && u.HasChildren)) ??
            storageUnits[0];
        if (!nextUnit) {
            return;
        }
        void navigate(InternalRoutes.Dashboard.ExploreStorageUnit.path, {
            replace: true,
            state: {
                unit: nextUnit,
                parentRef,
                trail,
            },
        });
    }, [loading, navigate, parentRef, selectedName, state?.unit, storageUnits, trail]);

    const filteredUnits = useMemo(() => storageUnits.filter(unit =>
        (unit.Name ?? "").toLowerCase().includes(search.trim().toLowerCase())
    ), [search, storageUnits]);

    // Group by the object's "Type" attribute (e.g. base table vs view); objects without one share a single group.
    const groupedUnits = useMemo(() => {
        const groups = new Map<string, SourceBrowserObject[]>();
        for (const unit of filteredUnits) {
            const type = unit.Attributes.find(attribute => attribute.Key === "Type")?.Value?.trim() ?? "";
            const list = groups.get(type) ?? [];
            list.push(unit);
            groups.set(type, list);
        }
        return [...groups.entries()];
    }, [filteredUnits]);

    const handleSelect = useCallback((tableId: string) => {
        if (tableId == null || tableId === activeUnitName) {
            return
        }
        const unit = storageUnits.find(u => u.Name === tableId);
        if (unit == null) {
            return;
        }
        if (unit.Actions.includes(SourceAction.Browse) && unit.HasChildren) {
            void navigate(InternalRoutes.Dashboard.StorageUnit.path, {
                state: {
                    parent: unit,
                    trail: [...trail, unit],
                },
            });
            return;
        }
        void navigate(InternalRoutes.Dashboard.ExploreStorageUnit.path, {
            state: {
                unit,
                parentRef,
                trail,
            },
        });
    }, [activeUnitName, navigate, parentRef, storageUnits, trail]);

    const handleDescribeUnit = useCallback((unit: SourceBrowserObject) => {
        setDescribeTargetUnit(unit);
        setDescribeOpen(true);
    }, []);

    const handleCopyTableName = useCallback((name: string) => {
        void navigator.clipboard.writeText(name).then(() => {
            toast.success(t('tableNameCopied'));
        });
    }, [t]);

    const handleDataCopy = useCallback(() => {
        void navigate(InternalRoutes.DataCopy.path);
    }, [navigate]);

    const handleConfirmTruncate = useCallback(async () => {
        if (!truncateTargetUnit) {
            return;
        }
        const target = truncateTargetUnit;
        try {
            const res = await clearSourceObjectData({
                variables: {
                    ref: {
                        Kind: target.Ref.Kind,
                        Locator: target.Ref.Locator,
                        Path: target.Ref.Path,
                    },
                },
            });
            if (res.data?.ClearSourceObjectData.Status) {
                toast.success(t('truncateSuccess', { table: target.Name }));
                setTruncateTargetUnit(null);
                await refetch();
                await apolloClient.refetchQueries({
                    include: [GetStorageUnitRowsDocument, GetStorageUnitsDocument],
                });
            } else {
                toast.error(t('truncateFailed', { error: '' }));
            }
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            toast.error(t('truncateFailed', { error: message }));
        }
    }, [apolloClient, clearSourceObjectData, refetch, t, truncateTargetUnit]);

    const handleConfirmDrop = useCallback(async () => {
        if (!dropTargetUnit) {
            return;
        }
        const target = dropTargetUnit;
        try {
            const res = await dropSourceObject({
                variables: {
                    ref: {
                        Kind: target.Ref.Kind,
                        Locator: target.Ref.Locator,
                        Path: target.Ref.Path,
                    },
                },
            });
            if (res.data?.DropSourceObject.Status) {
                toast.success(t('dropSuccess', { table: target.Name }));
                setDropTargetUnit(null);
                const result = await refetch();
                await apolloClient.refetchQueries({
                    include: [GetStorageUnitRowsDocument, GetStorageUnitsDocument],
                });
                if (activeUnitName === target.Name) {
                    const remaining = (result.data?.StorageUnit ?? []).filter(u => u.Name !== target.Name);
                    const nextUnit = remaining[0];
                    if (nextUnit) {
                        void navigate(InternalRoutes.Dashboard.ExploreStorageUnit.path, {
                            replace: true,
                            state: {
                                unit: nextUnit,
                                parentRef,
                                trail,
                            },
                        });
                    }
                }
            } else {
                toast.error(t('dropFailed', { error: '' }));
            }
        } catch (err) {
            const message = err instanceof Error ? err.message : String(err);
            toast.error(t('dropFailed', { error: message }));
        }
    }, [activeUnitName, apolloClient, dropSourceObject, dropTargetUnit, navigate, parentRef, refetch, t, trail]);

    const unitMenuLabels = useMemo(() => ({
        viewData: t('viewData'),
        describeTable: t('describeTable'),
        copyTableName: t('copyTableName'),
        copyTableData: t('copyTableData'),
        truncateTable: t('truncateTable'),
        dropTable: t('dropTable'),
    }), [t]);

    if (!current) {
        return null;
    }

    const describeUnitColumns = describeUnit ? storageUnitColumnsByName[describeUnit.Name] : undefined;

    return (
        <div className="flex h-full ce-schema-viewer" data-testid="schema-viewer">
            <SidebarComponent variant="embed" className="w-(--sidebar-width) h-full flex flex-col overflow-hidden">
                <div className="px-4 pt-3 pb-2.5 flex flex-col gap-2 shrink-0 border-b border-border/40">
                    <SearchInput
                        value={search}
                        onChange={(e) => { setSearch(e.target.value); }}
                        placeholder={t('searchTables')}
                        aria-label={t('searchTables')}
                    />
                    <div className="flex items-center gap-1.5" data-testid="schema-viewer-actions">
                        {canCreateObjects && (
                            <Button
                                variant="secondary"
                                size="sm"
                                className="flex-1 h-7 text-xs gap-1 px-2"
                                onClick={() => { setCreateOpen(true); }}
                                data-testid="create-storage-unit-button"
                            >
                                <PlusCircleIcon className="w-3.5 h-3.5 shrink-0" />
                                <span className="truncate">{tStorageUnit('newObject', { object: singularStorageUnitLabel.toLowerCase() })}</span>
                            </Button>
                        )}
                        {activeUnit != null && (
                            <Button
                                variant="outline"
                                size="sm"
                                className="h-7 text-xs gap-1 px-2 shrink-0"
                                onClick={() => {
                                    setDescribeTargetUnit(activeUnit);
                                    setDescribeOpen(true);
                                }}
                                data-testid="describe-storage-unit-button"
                            >
                                <InformationCircleIcon className="w-3.5 h-3.5 shrink-0" />
                                <span>{tStorageUnit('describe')}</span>
                            </Button>
                        )}
                        <Button
                            variant="ghost"
                            size="sm"
                            className="h-7 w-7 p-0 shrink-0"
                            onClick={() => { void refetch(); }}
                            title={tCommon('refresh')}
                            aria-label={tCommon('refresh')}
                            data-testid="refresh-storage-units-button"
                        >
                            <ArrowPathRoundedSquareIcon className="w-3.5 h-3.5" />
                        </Button>
                    </div>
                </div>
                <SidebarContent className="flex-1 min-h-0 overflow-y-auto">
                    <SidebarGroup>
                        {
                            loading ? (
                                <div className="flex-1 flex items-center justify-center">
                                    <Loading />
                                </div>
                            ) : filteredUnits.length === 0 ? (
                                <div className="flex-1 flex items-center justify-center px-4 text-center text-sm text-muted-foreground mt-4">
                                    {t('noResults')}
                                </div>
                            ) : (
                                <div className={`ce-schema-list ${ph.mask}`}>
                                    {groupedUnits.map(([type, units]) => <div key={type} className="ce-schema-group" data-testid="schema-viewer-group" data-group-type={type}>
                                        {units.map(unit => <SchemaViewerUnit
                                            key={unit.Name}
                                            unit={unit}
                                            selected={unit.Name === activeUnitName}
                                            onSelect={handleSelect}
                                            onDescribe={handleDescribeUnit}
                                            onCopyName={handleCopyTableName}
                                            onDataCopy={handleDataCopy}
                                            onTruncate={setTruncateTargetUnit}
                                            onDrop={setDropTargetUnit}
                                            labels={unitMenuLabels}
                                        />)}
                                    </div>)}
                                </div>
                            )
                        }
                    </SidebarGroup>
                </SidebarContent>
            </SidebarComponent>
            {canCreateObjects && (
                <Sheet open={createOpen} onOpenChange={setCreateOpen}>
                    <SheetContent side="right" className="p-0">
                        <div className="flex flex-col w-full justify-center p-8 h-full">
                            {createError && <Badge variant="destructive" className="mb-2 w-fit">{createError}</Badge>}
                            <CreateSourceObjectCard
                                databaseType={currentType}
                                parentRef={parentRef}
                                referenceColumnsByName={referenceColumnsByName}
                                referenceObjects={referenceStorageUnits.map(unit => ({ name: unit.Name }))}
                                singularStorageUnitLabel={singularStorageUnitLabel}
                                isOpen={createOpen}
                                onCreated={() => { void refetch(); }}
                                onErrorChange={setCreateError}
                                onClose={() => { setCreateOpen(false); }}
                            />
                        </div>
                    </SheetContent>
                </Sheet>
            )}
            {describeUnit != null && (
                <Sheet open={describeOpen} onOpenChange={setDescribeOpen}>
                    <SheetContent side="right" className="w-[min(768px,100vw)] sm:max-w-[min(768px,100vw)] p-0">
                        <div className="flex flex-col grow gap-lg justify-between p-8 h-full overflow-y-auto">
                            <div className="w-full" data-testid="explore-fields">
                                <SheetTitle className={cn("flex items-center gap-2 mb-4", ph.mask)}>
                                    <TableCellsIcon className="w-5 h-5" />
                                    {describeUnit.Name}
                                </SheetTitle>
                                {schemaFidelity === SourceSchemaFidelity.Sampled && (
                                    <div className="mb-2" data-testid="sampled-schema-warning">
                                        <div className="flex items-center gap-xs text-sm">
                                            <InformationCircleIcon className="w-4 h-4" />
                                            <span>{tStorageUnit('sampledSchemaBadge')}</span>
                                        </div>
                                    </div>
                                )}
                                <div className="flex flex-col gap-xs2">
                                    <StackList>
                                        {describeUnit.Attributes.map(attribute => {
                                            const rawDisplay = formatAttributeValue(attribute.Key, attribute.Value);
                                            const display = formatAttributeValue(attribute.Key, attribute.Value, tCommon);
                                            return (
                                                <div key={attribute.Key} data-field-key={attribute.Key} data-field-value={rawDisplay}>
                                                    <StackListItem rowClassName="text-base" item={formatAttributeKey(attribute.Key, tCommon)}>
                                                        {display}
                                                    </StackListItem>
                                                </div>
                                            );
                                        })}
                                    </StackList>
                                    {describeColumnsLoading && (
                                        <div className="mt-8">
                                            <Loading hideText={true} />
                                        </div>
                                    )}
                                    {!describeColumnsLoading && describeUnitColumns && describeUnitColumns.length > 0 && (
                                        <div className="mt-8">
                                            <div className="grid grid-cols-3 gap-4 mb-2 text-xs font-semibold uppercase text-muted-foreground">
                                                <span>{tCommon('columns')}</span>
                                                <span>{tCommon('comment')}</span>
                                                <span>{tCommon('attrType')}</span>
                                            </div>
                                            <StackList>
                                                {describeUnitColumns.map(col => {
                                                    const isForeignKey = col.IsForeignKey;
                                                    return (
                                                        <div key={col.Name} className={ph.mask} data-field-key={col.Name} data-field-value={col.Type?.toLowerCase()} data-is-foreign-key={isForeignKey || undefined}>
                                                            <div data-slot="stack-list-item" className="flex flex-col">
                                                                <p className="grid grid-cols-3 items-center text-base py-3 min-w-0 gap-4">
                                                                    <span className="font-semibold truncate" title={col.Name}>
                                                                        {isForeignKey ?
                                                                            <Badge className="text-base" data-testid="foreign-key-attribute" variant="secondary">{col.Name}</Badge> : col.Name}
                                                                    </span>
                                                                    <span className="text-muted-foreground truncate min-w-0" title={col.Comment ?? undefined}>
                                                                        {col.Comment ?? '-'}
                                                                    </span>
                                                                    <span className="truncate min-w-0" title={col.Type?.toLowerCase()}>
                                                                        {col.Type?.toLowerCase()}
                                                                    </span>
                                                                </p>
                                                            </div>
                                                        </div>
                                                    );
                                                })}
                                            </StackList>
                                        </div>
                                    )}
                                </div>
                            </div>
                        </div>
                    </SheetContent>
                </Sheet>
            )}
            <AlertDialog open={truncateTargetUnit != null} onOpenChange={(open) => { if (!open) setTruncateTargetUnit(null); }}>
                <AlertDialogContent>
                    <AlertDialogHeader>
                        <AlertDialogTitle>{t('truncateConfirmTitle')}</AlertDialogTitle>
                        <AlertDialogDescription>
                            {t('truncateConfirmDescription', { table: truncateTargetUnit?.Name ?? '' })}
                        </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                        <AlertDialogCancel disabled={truncating}>{t('cancel')}</AlertDialogCancel>
                        <AlertDialogAction asChild>
                            <Button
                                variant="destructive"
                                disabled={truncating}
                                onClick={() => { void handleConfirmTruncate(); }}
                            >
                                {t('confirm')}
                            </Button>
                        </AlertDialogAction>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialog>
            <AlertDialog open={dropTargetUnit != null} onOpenChange={(open) => { if (!open) setDropTargetUnit(null); }}>
                <AlertDialogContent>
                    <AlertDialogHeader>
                        <AlertDialogTitle>{t('dropConfirmTitle')}</AlertDialogTitle>
                        <AlertDialogDescription>
                            {t('dropConfirmDescription', { table: dropTargetUnit?.Name ?? '' })}
                        </AlertDialogDescription>
                    </AlertDialogHeader>
                    <AlertDialogFooter>
                        <AlertDialogCancel disabled={dropping}>{t('cancel')}</AlertDialogCancel>
                        <AlertDialogAction asChild>
                            <Button
                                variant="destructive"
                                disabled={dropping}
                                onClick={() => { void handleConfirmDrop(); }}
                            >
                                {t('confirm')}
                            </Button>
                        </AlertDialogAction>
                    </AlertDialogFooter>
                </AlertDialogContent>
            </AlertDialog>
        </div>
    );
};
