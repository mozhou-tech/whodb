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

import type {SQLNamespace} from "@codemirror/lang-sql";
import {
    AlertDialog,
    AlertDialogAction,
    AlertDialogCancel,
    AlertDialogContent,
    AlertDialogDescription,
    AlertDialogFooter,
    AlertDialogHeader,
    AlertDialogTitle,
    AlertDialogTrigger,
    Badge,
    Button,
    Card,
    cn,
    CopyButton,
    EmptyState,
    formatDate,
    Input,
    Label,
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    Sheet,
    SheetContent,
    SheetFooter,
    SheetTitle,
    StackList,
    StackListItem,
    toast,
    IconRefresh as ArrowPathIcon,
    IconClock as ClockIcon,
    IconTerminal as CommandLineIcon,
    IconEdit as PencilIcon,
    IconPlay as PlayIcon,
    IconTrash as TrashIcon,
    IconClose as XMarkIcon,
} from '@clidey/ux';
import {skipToken, useLazyQuery, useMutation, useQuery} from "@apollo/client/react";
import type {
    GetStorageUnitsQuery,
    RecordInput,
    RowsResult,
    SortCondition,
    SourceObjectRefInput,
    WhereCondition} from '@graphql';
import {
    AddRowDocument,
    ColumnsDocument,
    DataShape,
    GetSourceContentDocument,
    GetStorageUnitRowsDocument,
    GetStorageUnitsDocument,
    RawExecuteDocument,
    SortDirection,
    SourceAction,
    SourceFieldConstraintsDocument,
    UpdateStorageUnitDocument,
    WhereConditionType
} from '@graphql';
import type {FC} from "react";
import { useCallback, useEffect, useMemo, useRef, useState} from "react";
import {useLocation} from "react-router-dom";
import {CodeEditor} from "../../components/editor";
import {ErrorState} from "../../components/error-state";
import {Loading, LoadingPage, Spinner} from "../../components/loading";
import {InternalPage} from "../../components/page";
import {SchemaViewer} from "../../components/schema-viewer";
import {getInputPropsForColumnType, StorageUnitTable} from "../../components/table";
import {WhoDBChatIcon} from "../../components/whodb-chat-icon";
import {Tip} from "../../components/tip";
import {InternalRoutes} from "../../config/routes";
import {useSourceContract} from "../../hooks/useSourceContract";
import {useAppDispatch, useAppSelector} from "../../store/hooks";
import {ExploreConditionsActions} from "../../store/explore-conditions";
import {usePageSize} from "../../hooks/use-page-size";
import {useTranslation} from "../../hooks/use-translation";
import {isDestructiveQuery} from "../../utils/query-utils";
import { findSourceObjectType } from "../../config/source-types";
import {buildSourceObjectRef, buildSourceParentRef} from "../../utils/source-refs";
import {formatAttributeKey, formatAttributeValue} from "../../utils/functions";
import {ph} from "../../utils/privacy";

type SourceBrowserObject = GetStorageUnitsQuery['StorageUnit'][number];

const lastSelectedUnitByScope = new Map<string, SourceBrowserObject>();
const lastRowsByConditionKey = new Map<string, RowsResult>();

const ForeignKeyAddRowField: FC<{
    reference: SourceObjectRefInput;
    column: string;
    fieldName: string;
    value: string;
    onChange: (value: string) => void;
}> = ({reference, column, fieldName, value, onChange}) => {
    const {t} = useTranslation('pages/explore-storage-unit');
    const [manual, setManual] = useState(false);
    const {data, loading} = useQuery(GetStorageUnitRowsDocument, {
        variables: {ref: reference, pageSize: 100, pageOffset: 0},
    });
    const columnIndex = data?.Row.Columns.findIndex(item => item.Name === column) ?? -1;
    const values = columnIndex < 0 ? [] : [...new Set(data?.Row.Rows.map(row => row[columnIndex]).filter(Boolean) ?? [])];

    return <>
        <Select value={manual ? '__manual__' : value || undefined} onValueChange={nextValue => {
            if (nextValue === '__manual__') {
                setManual(true);
                onChange('');
            } else {
                setManual(false);
                onChange(nextValue);
            }
        }}>
            <SelectTrigger className="w-full" data-testid={`add-row-fk-${fieldName}`}>
                <span>{loading ? t('loadingReferencedRows') : manual ? t('enterReferencedKey') : value || t('selectReferencedRow')}</span>
            </SelectTrigger>
            <SelectContent>
                {values.map(option => <SelectItem key={option} value={option} data-value={option}>{option}</SelectItem>)}
                <SelectItem value="__manual__" data-value="__manual__">{t('enterReferencedKey')}</SelectItem>
            </SelectContent>
        </Select>
        {manual && <Input value={value} onChange={event => {onChange(event.target.value);}} placeholder={t('enterReferencedKey')} aria-label={t('enterReferencedKey')} />}
    </>;
};

type ExploreSourceState = {
    unit?: SourceBrowserObject;
    parentRef?: SourceObjectRefInput;
    trail?: SourceBrowserObject[];
};

const mongoIdentifierPattern = /^[A-Za-z_]\w*$/;
const redisSimpleArgumentPattern = /^[^\s'"]+$/;

function buildMongoScratchpadQuery(collectionName: string): string {
    if (mongoIdentifierPattern.test(collectionName)) {
        return `db.${collectionName}.find({})`;
    }
    return `db.getCollection(${JSON.stringify(collectionName)}).find({})`;
}

function buildRedisScratchpadQuery(keyName: string): string {
    if (redisSimpleArgumentPattern.test(keyName)) {
        return `TYPE ${keyName}`;
    }
    return `TYPE ${JSON.stringify(keyName)}`;
}

export const ExploreStorageUnit: FC = () => {
    const location = useLocation();
    const defaultPageSize = useAppSelector(state => state.settings.defaultPageSize);
    const maxPageSize = useAppSelector(state => state.settings.maxPageSize);
    const pageSizeOptions = useMemo(() => ({ maxPageSize }), [maxPageSize]);
    const { pageSize } = usePageSize(defaultPageSize, pageSizeOptions);
    const { t } = useTranslation('pages/explore-storage-unit');

    let schema = useAppSelector(state => state.database.schema);
    const current = useAppSelector(state => state.auth.current);
    const currentType = current?.Type;
    const {
        item,
        isNoSQL,
        storageUnitLabel,
        supportsScratchpad,
        supportsSchema,
        usesDatabaseInsteadOfSchema,
    } = useSourceContract(currentType);
    const formatDatesLocale = useAppSelector(state => state.settings.formatDatesLocale);
    const formatBooleansReadable = useAppSelector(state => state.settings.formatBooleansReadable);
    const locationState = location.state as ExploreSourceState | undefined;
    const browserTrail = locationState?.trail ?? [];

    // TODO: ClickHouse/MongoDB use database name as schema parameter since they lack traditional schemas
    if (usesDatabaseInsteadOfSchema && current?.Database) {
        schema = current.Database;
    }

    const currentParentRef = useMemo(() => {
        if (browserTrail.length > 0 && locationState?.parentRef) {
            return locationState.parentRef;
        }
        return buildSourceParentRef(item, current, schema);
    }, [browserTrail.length, current, item, locationState?.parentRef, schema]);

    const storageUnitsQueryOptions = current && (locationState?.parentRef != null || !supportsSchema || schema !== "")
        ? {
            variables: {
                parent: currentParentRef,
            },
        }
        : skipToken;
    const { data: storageUnitsData } = useQuery(GetStorageUnitsDocument, storageUnitsQueryOptions);
    const storageUnits = useMemo(
        () => (storageUnitsData?.StorageUnit ?? []) as SourceBrowserObject[],
        [storageUnitsData?.StorageUnit]
    );

    const scopeKey = `${current?.Id ?? ""}__${current?.Database ?? ""}__${schema ?? ""}`;
    const unit = useMemo(() => {
        if (locationState?.unit) {
            const stateUnit = locationState.unit;
            if (storageUnits.length === 0 || storageUnits.some(u => u.Name === stateUnit.Name)) {
                return stateUnit;
            }
        }
        const remembered = lastSelectedUnitByScope.get(scopeKey);
        if (remembered && (storageUnits.length === 0 || storageUnits.some(u => u.Name === remembered.Name))) {
            return storageUnits.find(u => u.Name === remembered.Name) ?? remembered;
        }
        return (
            storageUnits.find(u => !(u.Actions.includes(SourceAction.Browse) && u.HasChildren)) ??
            storageUnits[0]
        );
    }, [locationState?.unit, scopeKey, storageUnits]);

    useEffect(() => {
        if (unit && scopeKey) {
            lastSelectedUnitByScope.set(scopeKey, unit);
        }
    }, [scopeKey, unit]);

    const currentObjectType = useMemo(() => findSourceObjectType(item, unit?.Kind), [item, unit?.Kind]);
    const currentObjectActions = unit?.Actions ?? currentObjectType?.Actions ?? [];
    const canViewRows = currentObjectActions.includes(SourceAction.ViewRows);
    const canViewContent = currentObjectActions.includes(SourceAction.ViewContent);
    const isTabularObject = currentObjectType?.DataShape === DataShape.Tabular || currentObjectType?.DataShape === DataShape.Document || canViewRows;
    const isContentObject = !isTabularObject && (currentObjectType?.DataShape === DataShape.Content || canViewContent);
    const allowsInsertData = currentObjectActions.includes(SourceAction.InsertData);
    const allowsUpdateData = currentObjectActions.includes(SourceAction.UpdateData);
    const allowsDeleteData = currentObjectActions.includes(SourceAction.DeleteData);

    const dispatch = useAppDispatch();
    const conditionKey = useMemo(() => {
        if (!current?.Id || !unit?.Name) return '';
        return `${current.Id}__${schema}__${unit.Name}`;
    }, [current?.Id, schema, unit?.Name]);
    const savedConditions = useAppSelector(state =>
        conditionKey ? state.exploreConditions.conditions[conditionKey] : undefined
    );
    const savedLastSql = useAppSelector(state =>
        conditionKey ? state.exploreConditions.lastSqlByKey?.[conditionKey] : undefined
    );
    const allSqlHistory = useAppSelector(state => state.exploreConditions.sqlHistory ?? []);
    const currentDbName = current?.Database ?? schema ?? '';
    const sqlHistory = useMemo(() => {
        const currentTable = unit?.Name;
        if (!currentTable) {
            return [];
        }
        const seen = new Set<string>();
        return allSqlHistory.filter(historyItem => {
            if (historyItem.table !== currentTable) {
                return false;
            }
            if (historyItem.database && currentDbName && historyItem.database !== currentDbName) {
                return false;
            }
            const dedupKey = historyItem.sql.trim().replace(/;+\s*$/, '').replace(/\s+/g, ' ');
            if (!dedupKey || seen.has(dedupKey)) {
                return false;
            }
            seen.add(dedupKey);
            return true;
        });
    }, [allSqlHistory, currentDbName, unit?.Name]);

    const [currentPage, setCurrentPage] = useState(1);
    const [whereCondition, setWhereCondition] = useState<WhereCondition | undefined>(savedConditions?.whereCondition);
    const [sortConditions, setSortConditions] = useState<SortCondition[]>(savedConditions?.sortConditions ?? []);
    const [isSqlHistoryOpen, setIsSqlHistoryOpen] = useState(false);
    const [activeSqlQuery, setActiveSqlQuery] = useState<string | null>(null);
    const [sqlError, setSqlError] = useState<string | null>(null);
    const [tableActionsTarget, setTableActionsTarget] = useState<HTMLDivElement | null>(null);
    const baseTableColumnsRef = useRef<RowsResult['Columns']>([]);
    const [baseTableColumns, setBaseTableColumns] = useState<RowsResult['Columns']>([]);

    // Check if this is a view/materialized view - mock data generation not allowed for these
    const isMockDataGenerationAllowed = useMemo(() => {
        if (!unit?.Attributes) return true;
        const typeAttr = unit.Attributes.find(attr => attr.Key === "Type");
        if (!typeAttr) return true;
        const upperType = typeAttr.Value.toUpperCase();
        return !upperType.includes("VIEW") && !upperType.includes("MATERIALIZED") && !upperType.includes("SYSTEM");
    }, [unit?.Attributes]);

    const [rows, setRows] = useState<RowsResult | undefined>(() =>
        conditionKey ? lastRowsByConditionKey.get(conditionKey) : undefined
    );
    const [showAdd, setShowAdd] = useState(false);
    const searchRef = useRef<(search: string) => void>(() => {});

    // Request counter to prevent race conditions - only the latest query's results should be used
    const latestRequestIdRef = useRef(0);
    // Ref to always have the latest whereCondition (avoids stale closure issues)
    const whereConditionRef = useRef<WhereCondition | undefined>(whereCondition);
    const [currentTableName, setCurrentTableName] = useState<string>("");
    
    // For add row sheet logic
    const [addRowData, setAddRowData] = useState<Record<string, any>>({});
    const [addRowError, setAddRowError] = useState<string | null>(null);
    const [fetchConstraints, { data: constraintsData }] = useLazyQuery(SourceFieldConstraintsDocument);

    // Entity search sheet state
    const [showEntitySearchSheet, setShowEntitySearchSheet] = useState(false);
    const [entitySearchData, setEntitySearchData] = useState<{
        columnName: string;
        value: string;
        targetTable: string;
    } | null>(null);
    const [entitySearchResults, setEntitySearchResults] = useState<RowsResult | null>(null);

    // Track pending add-row timeout so it can be cancelled on unmount
    const addRowTimeoutRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

    // Track table container height for responsive sizing
    const tableContainerRef = useRef<HTMLDivElement>(null);
    const pageSizeInitialRef = useRef(true);
    const [tableHeight, setTableHeight] = useState<number>(500);

    const [updateStorageUnit] = useMutation(UpdateStorageUnitDocument);

    // Keep whereConditionRef in sync with whereCondition state
    useEffect(() => {
        whereConditionRef.current = whereCondition;
    }, [whereCondition]);


    // Clean up pending add-row timeout on unmount
    useEffect(() => {
        return () => {
            clearTimeout(addRowTimeoutRef.current);
        };
    }, []);

    const [getStorageUnitRows, { loading }] = useLazyQuery(GetStorageUnitRowsDocument, {
        fetchPolicy: "no-cache",
    });
    const [getColumns] = useLazyQuery(ColumnsDocument, {
        fetchPolicy: "network-only",
    });
    const [addRow, { loading: adding }] = useMutation(AddRowDocument);
    const [rawExecute, { loading: rawExecuteLoading }] = useLazyQuery(RawExecuteDocument, {
        fetchPolicy: "no-cache",
    });

    const unitName = useMemo(() => {
        return unit?.Name;
    }, [unit]);
    const currentUnitRef = unit?.Ref;
    const sourceContentQueryOptions = currentUnitRef && isContentObject
        ? {
            variables: { ref: currentUnitRef },
            fetchPolicy: "cache-first" as const,
        }
        : skipToken;
    const { data: sourceContentData, loading: sourceContentLoading } = useQuery(GetSourceContentDocument, sourceContentQueryOptions);
    const sourceContent = sourceContentData?.Content;
    const sourceContentLanguage = useMemo(() => {
        if (!sourceContent?.Text) {
            return undefined;
        }
        if (sourceContent.MIMEType.includes("json")) {
            return "json" as const;
        }
        if (sourceContent.MIMEType.includes("markdown")) {
            return "markdown" as const;
        }
        return undefined;
    }, [sourceContent?.MIMEType, sourceContent?.Text]);

    const initialScratchpadQuery = useMemo(() => {
        const scratchpadUnitName = unitName ?? currentTableName;
        if (!scratchpadUnitName) {
            return "";
        }
        if (currentType === "MongoDB") {
            return buildMongoScratchpadQuery(scratchpadUnitName);
        }
        if (currentType === "Redis") {
            return buildRedisScratchpadQuery(scratchpadUnitName);
        }
        const qualified = schema ? `${schema}.${scratchpadUnitName}` : scratchpadUnitName;
        return `SELECT * FROM ${qualified} LIMIT ${pageSize}`;
    }, [currentTableName, currentType, pageSize, schema, unitName]);

    const [code, setCode] = useState((savedLastSql?.trim() ? savedLastSql : undefined) ?? initialScratchpadQuery);

    const handleCodeChange = useCallback((nextCode: string) => {
        setCode(nextCode);
        if (conditionKey) {
            dispatch(ExploreConditionsActions.setLastSql({
                key: conditionKey,
                sql: nextCode,
            }));
        }
    }, [conditionKey, dispatch]);

    const sqlSchema = useMemo<SQLNamespace>(() => {
        const tableDetail = t('completionTable');
        const columnDetail = t('completionColumn');
        const entries: SQLNamespace = {};
        for (const u of storageUnits) {
            entries[u.Name] = {
                self: { label: u.Name, type: 'type', detail: tableDetail },
                children: u.Name === unitName
                    ? baseTableColumns.map(col => ({ label: col.Name, type: 'property', detail: columnDetail }))
                    : [],
            };
        }
        return entries;
    }, [baseTableColumns, storageUnits, t, unitName]);

    const displayedRows = useMemo(() => {
        if (!rows) {
            return [];
        }
        if (activeSqlQuery != null && rows.Rows.length > pageSize) {
            const start = (currentPage - 1) * pageSize;
            return rows.Rows.slice(start, start + pageSize);
        }
        return rows.Rows;
    }, [activeSqlQuery, currentPage, pageSize, rows]);

    // Compute columns and types from rows data
    const {columns, columnTypes, columnComments, columnIsPrimary, columnIsForeignKey} = useMemo(() => {
        const dataColumns = rows?.Columns.map(c => c.Name) ?? [];
        return {
            columns: dataColumns,
            columnTypes: rows?.Columns.map(column => column.Type),
            columnComments: rows?.Columns.map(column => column.Comment ?? undefined),
            columnIsPrimary: rows?.Columns.map(column => column.IsPrimary),
            columnIsForeignKey: rows?.Columns.map(column => column.IsForeignKey)
        };
    }, [rows?.Columns]);

    const handleSubmitRequest = useCallback((pageOffset: number | null = null) => {
        if (!isTabularObject) {
            return;
        }
        const tableNameToUse = unitName ?? currentTableName;
        if (tableNameToUse) {
            setCurrentTableName(tableNameToUse);
        }
        // Increment request counter and capture this request's ID to prevent race conditions
        latestRequestIdRef.current += 1;
        const thisRequestId = latestRequestIdRef.current;
        // Use ref to always get the latest whereCondition (avoids stale closure issues)
        const currentWhereCondition = whereConditionRef.current;
        const ref = tableNameToUse === unitName
            ? currentUnitRef
            : buildSourceObjectRef(item, current, schema, tableNameToUse);
        if (!ref) {
            return;
        }

        void getStorageUnitRows({
            variables: {
                ref,
                where: currentWhereCondition,
                sort: sortConditions.length > 0 ? sortConditions : undefined,
                pageSize,
                pageOffset: pageOffset ?? (currentPage - 1) * pageSize,
            },
        }).then(result => {
            const isLatest = thisRequestId === latestRequestIdRef.current;
            if (isLatest && result.data) {
                setActiveSqlQuery(null);
                setSqlError(null);
                baseTableColumnsRef.current = result.data.Row.Columns;
                setBaseTableColumns(result.data.Row.Columns);
                setRows(result.data.Row);
                if (conditionKey) {
                    lastRowsByConditionKey.set(conditionKey, result.data.Row);
                }
            }
        });
    }, [conditionKey, current, currentPage, currentTableName, currentUnitRef, getStorageUnitRows, isTabularObject, item, pageSize, schema, sortConditions, unitName]);

    const handlePageChange = useCallback((page: number) => {
        setCurrentPage(page);
        if (activeSqlQuery != null) {
            return;
        }
        handleSubmitRequest((page - 1) * pageSize);
    }, [activeSqlQuery, handleSubmitRequest, pageSize]);

    const handleColumnSort = useCallback((columnName: string) => {
        const existingSort = sortConditions.find(s => s.Column === columnName);
        let newSort: SortCondition[];
        if (!existingSort) {
            newSort = [...sortConditions, { Column: columnName, Direction: SortDirection.Asc }];
        } else if (existingSort.Direction === SortDirection.Asc) {
            newSort = sortConditions.map(s => s.Column === columnName ? { ...s, Direction: SortDirection.Desc } : s);
        } else {
            newSort = sortConditions.filter(s => s.Column !== columnName);
        }
        setSortConditions(newSort);
        if (conditionKey) {
            dispatch(ExploreConditionsActions.setExploreConditions({
                key: conditionKey,
                whereCondition,
                sortConditions: newSort,
            }));
        }
    }, [sortConditions, conditionKey, whereCondition, dispatch]);

    const handleRowUpdate = useCallback(
        (
            row: Record<string, string | number>,
            originalRow?: Record<string, string | number>
        ) => {
            if (!current) {
                return Promise.resolve();
            }

            return new Promise<void>((resolve, reject) => {
                // Figure out which columns to update
                const changedColumns = originalRow
                    ? Object.keys(row).filter((col) => row[col] !== originalRow[col])
                    : Object.keys(row);

                if (changedColumns.length === 0) {
                    // Nothing changed, skip
                    resolve();
                    return;
                }

                // Build values for all columns
                const allColumns = Object.keys(row);
                const values = allColumns.map((col) => ({
                    Key: col,
                    Value: row[col].toString(),
                }));
                if (!currentUnitRef) {
                    reject(new Error("Missing source object ref"));
                    return;
                }

                void updateStorageUnit({
                    variables: {
                        ref: currentUnitRef,
                        values,
                        updatedColumns: changedColumns,
                    },
                    onCompleted: (data) => {
                        if (!data?.UpdateStorageUnit.Status) {
                            reject(new Error("Update failed"));
                            return;
                        }
                        resolve();
                    },
                    onError: (error) => {
                        reject(error);
                    },
                });
            });
        },
        [current, currentUnitRef]
    );

    const totalCount = useMemo(() => {
        if (activeSqlQuery != null) {
            return (rows?.Rows.length ?? 0).toString();
        }
        if (rows?.TotalCount != null && rows.TotalCount > 0) {
            return rows.TotalCount.toString();
        }
        const count = unit?.Attributes.find(attribute => attribute.Key === "Count")?.Value;
        if (count != null && count !== "0" && count !== "unknown") {
            return count;
        }
        return rows?.Rows.length?.toString() ?? "unknown";
    }, [activeSqlQuery, unit, rows?.TotalCount, rows?.Rows.length]);

    useEffect(() => {
        // Reset non-condition state when switching to a new table; preserve cached rows if available
        setCurrentPage(1);
        setActiveSqlQuery(null);
        setSqlError(null);
        const cachedRows = conditionKey ? lastRowsByConditionKey.get(conditionKey) : undefined;
        setRows(cachedRows);
        if (cachedRows?.Columns) {
            baseTableColumnsRef.current = cachedRows.Columns;
            setBaseTableColumns(cachedRows.Columns);
        }
        setShowAdd(false);
        setAddRowData({});
        setAddRowError(null);
        setShowEntitySearchSheet(false);
        setEntitySearchData(null);
        setEntitySearchResults(null);
        searchRef.current("");

        // Restore saved conditions for this unit, or reset to empty if none saved
        const restoredWhere = savedConditions?.whereCondition;
        const restoredSort = savedConditions?.sortConditions ?? [];
        setWhereCondition(restoredWhere);
        whereConditionRef.current = restoredWhere;
        setSortConditions(restoredSort);

        // Fetch fresh data for tabular objects only.
        if (isTabularObject) {
            handleSubmitRequest();
        }
        setCode((savedLastSql?.trim() ? savedLastSql : undefined) ?? initialScratchpadQuery);
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [isTabularObject, unit?.Name, conditionKey]);

    useEffect(() => {
        if (isTabularObject && sortConditions.length > 0) {
            handleSubmitRequest();
        }
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [isTabularObject, sortConditions]);

    useEffect(() => {
        if (pageSizeInitialRef.current) {
            pageSizeInitialRef.current = false;
            return;
        }
        if (!isTabularObject) {
            return;
        }
        setCurrentPage(1);
        handleSubmitRequest(0);
    // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [isTabularObject, pageSize]);

    const routes = useMemo(() => {
        return [
            {
                ...InternalRoutes.Dashboard.StorageUnit,
                name: storageUnitLabel,
            },
            {
                ...InternalRoutes.Dashboard.ExploreStorageUnit,
                name: unit?.Name ?? InternalRoutes.Dashboard.ExploreStorageUnit.name,
            },
        ];
    }, [storageUnitLabel, unit?.Name]);

    // Broadcast available columns for Command Palette sorting
    useEffect(() => {
        window.dispatchEvent(new CustomEvent('table:columns-available', {
            detail: { columns }
        }));
        return () => {
            window.dispatchEvent(new CustomEvent('table:columns-available', {
                detail: { columns: [] }
            }));
        };
    }, [columns]);

    // Listen for sort requests from Command Palette
    useEffect(() => {
        const handleSortColumn = (event: CustomEvent<{ column: string }>) => {
            handleColumnSort(event.detail.column);
        };
        window.addEventListener('table:sort-column', handleSortColumn as EventListener);
        return () => {
            window.removeEventListener('table:sort-column', handleSortColumn as EventListener);
        };
    }, [handleColumnSort]);

    // Dynamically adjust table height based on available viewport space
    useEffect(() => {
        const updateTableHeight = () => {
            if (tableContainerRef.current) {
                // Get the position of the table container relative to viewport
                const rect = tableContainerRef.current.getBoundingClientRect();
                // Calculate available height from current position to bottom of viewport
                // Subtract bottom padding (40px for spacing)
                const availableHeight = window.innerHeight - rect.top - 40;

                // Reserve space for table controls:
                // - Table header row: ~48px
                // - Add Row button and children: ~60px
                // - Pagination controls: ~48px
                // - Export button: ~60px
                // - Internal gaps and spacing: ~44px
                const reservedSpace = 260;

                // Calculate final height with minimum of 300px for usability
                const calculatedHeight = Math.max(300, availableHeight - reservedSpace);
                setTableHeight(calculatedHeight);
            }
        };

        // Initial calculation with a small delay to ensure DOM is ready
        const timeoutId = setTimeout(updateTableHeight, 0);

        // Update on window resize
        window.addEventListener('resize', updateTableHeight);

        // Update when content changes (using ResizeObserver for container)
        const resizeObserver = new ResizeObserver(updateTableHeight);
        if (tableContainerRef.current) {
            resizeObserver.observe(tableContainerRef.current);
        }

        return () => {
            clearTimeout(timeoutId);
            window.removeEventListener('resize', updateTableHeight);
            resizeObserver.disconnect();
        };
    }, [rows]);

    const sortedColumnsMap = useMemo(() => {
        const map = new Map<string, 'asc' | 'desc'>();
        sortConditions.forEach(cond => {
            map.set(cond.Column, cond.Direction === SortDirection.Asc ? 'asc' : 'desc');
        });
        return map;
    }, [sortConditions]);

    // Sheet logic for Add Row (like table.tsx)
    const handleOpenAddSheet = useCallback(() => {
        const initialData: Record<string, any> = {};
        if (rows?.Columns) {
            for (const col of rows.Columns) {
                initialData[col.Name] = "";
            }
        }
        setAddRowData(initialData);
        setAddRowError(null);
        if (currentUnitRef) {
            void fetchConstraints({ variables: { ref: currentUnitRef } });
        }
        setShowAdd(true);
    }, [rows?.Columns, currentUnitRef, fetchConstraints]);

    const handleAddRowFieldChange = useCallback((key: string, value: string) => {
        setAddRowData(prev => ({
            ...prev,
            [key]: value,
        }));
    }, []);

    const handleAddRowSubmit = useCallback(() => {
        if (rows?.Columns == null) return;
        // Document sources take a single JSON value, so per-field required checks don't apply.
        const isDocumentInput = isNoSQL && rows.Columns.length === 1 && rows.Columns[0].Type === "Document";
        const missingRequired = !isDocumentInput && constraintsData?.SourceFieldConstraints?.some(constraint =>
            constraint.Nullable !== true && !constraint.Identity && constraint.DefaultValue == null && !addRowData[constraint.Name]
        );
        if (missingRequired) {
            setAddRowError(t('fillRequiredFields'));
            return;
        }
        const values: RecordInput[] = [];
        if (isDocumentInput) {
            try {
                const json = JSON.parse(addRowData.document);
                for (const key of Object.keys(json)) {
                    const val = json[key];
                    // Convert non-string values to string for GraphQL
                    const stringValue = typeof val === 'object' && val !== null
                        ? JSON.stringify(val)
                        : val;
                    values.push({
                        Key: key,
                        Value: stringValue,
                    });
                }
            } catch {
                setAddRowError(t('invalidJson'));
                return;
            }
        } else {
            for (const col of rows.Columns) {
                if (addRowData[col.Name] !== undefined && addRowData[col.Name] !== "") {
                    values.push({
                        Key: col.Name,
                        Value: addRowData[col.Name],
                    });
                }
            }
        }

        if (values.length === 0) {
            setAddRowError(t('fillAtLeastOneValue'));
            return;
        }
        void addRow({
            variables: {
                ref: currentUnitRef ?? buildSourceObjectRef(item, current, schema, unit?.Name ?? unitName ?? currentTableName ?? ""),
                values,
            },
            onCompleted() {
                toast.success(t('addRowSuccess'));
                setShowAdd(false);
                addRowTimeoutRef.current = setTimeout(() => {
                    handleSubmitRequest();
                }, 500);
            },
            onError(e) {
                setAddRowError(e.message);
                const errorMessage = t('addRowError').replace('{error}', e.message);
                toast.error(errorMessage);
            },
        });
    }, [addRow, addRowData, constraintsData?.SourceFieldConstraints, current, currentTableName, currentUnitRef, handleSubmitRequest, isNoSQL, item, rows?.Columns, schema, t, unit?.Name, unitName]);

    const [pendingScratchpadQuery, setPendingScratchpadQuery] = useState<string | null>(null);

    const doScratchpadExecute = useCallback((query: string) => {
        const trimmed = query.trim();
        if (!trimmed) {
            return;
        }
        setSqlError(null);
        latestRequestIdRef.current += 1;
        const thisRequestId = latestRequestIdRef.current;
        void rawExecute({
            variables: { query: trimmed },
        }).then(result => {
            if (thisRequestId !== latestRequestIdRef.current) {
                return;
            }
            if (result.error) {
                setSqlError(result.error.message);
                dispatch(ExploreConditionsActions.addSqlHistory({
                    sql: trimmed,
                    table: unitName,
                    database: current?.Database ?? schema,
                    status: false,
                }));
                return;
            }
            const rawResult = result.data?.RawExecute;
            if (!rawResult) {
                return;
            }
            dispatch(ExploreConditionsActions.addSqlHistory({
                sql: trimmed,
                table: unitName,
                database: current?.Database ?? schema,
                status: true,
                rowCount: rawResult.Rows.length,
            }));
            if (conditionKey) {
                dispatch(ExploreConditionsActions.setLastSql({
                    key: conditionKey,
                    sql: trimmed,
                }));
            }
            if (rawResult.Columns.length === 0 && isDestructiveQuery(trimmed, current?.Type)) {
                toast.success(t('sqlExecutedSuccess'));
                setActiveSqlQuery(null);
                handleSubmitRequest(0);
                return;
            }
            const knownColumnsByName = new Map(
                baseTableColumnsRef.current.map(col => [col.Name, col])
            );
            const enrichedColumns = rawResult.Columns.map(col => {
                const known = knownColumnsByName.get(col.Name);
                if (!known) {
                    return col;
                }
                return {
                    ...col,
                    Comment: col.Comment ?? known.Comment,
                    IsPrimary: col.IsPrimary || known.IsPrimary,
                    IsForeignKey: col.IsForeignKey || known.IsForeignKey,
                    ReferencedTable: col.ReferencedTable ?? known.ReferencedTable,
                    ReferencedColumn: col.ReferencedColumn ?? known.ReferencedColumn,
                    Length: col.Length ?? known.Length,
                    Precision: col.Precision ?? known.Precision,
                    Scale: col.Scale ?? known.Scale,
                };
            });
            setCurrentPage(1);
            setActiveSqlQuery(trimmed);
            setRows({
                ...rawResult,
                Columns: enrichedColumns,
            });
        }).catch((err: unknown) => {
            const message = err instanceof Error ? err.message : String(err);
            setSqlError(message);
            dispatch(ExploreConditionsActions.addSqlHistory({
                sql: trimmed,
                table: unitName,
                database: current?.Database ?? schema,
                status: false,
            }));
        });
    }, [conditionKey, current?.Database, current?.Type, dispatch, handleSubmitRequest, rawExecute, schema, t, unitName]);

    const handleScratchpad = useCallback((specificCode?: string) => {
        if (current == null) {
            return;
        }
        const query = (specificCode ?? code).trim();
        if (!query) {
            return;
        }
        if (isDestructiveQuery(query, current.Type)) {
            setPendingScratchpadQuery(query);
        } else {
            doScratchpadExecute(query);
        }
    }, [code, current, doScratchpadExecute]);

    const handleResetSqlResult = useCallback(() => {
        setActiveSqlQuery(null);
        setSqlError(null);
        setCurrentPage(1);
        handleSubmitRequest(0);
    }, [handleSubmitRequest]);

    const handleTableRefresh = useCallback(() => {
        if (activeSqlQuery != null) {
            doScratchpadExecute(activeSqlQuery);
            return;
        }
        handleSubmitRequest();
    }, [activeSqlQuery, doScratchpadExecute, handleSubmitRequest]);


    // Foreign key detection using actual column metadata
    const getColumnByName = useCallback((columnName: string) => {
        return rows?.Columns?.find(col => col.Name === columnName);
    }, [rows?.Columns]);

    const isValidForeignKey = useCallback((columnName: string) => {
        const column = getColumnByName(columnName);
        return Boolean(column?.IsForeignKey && column?.ReferencedTable != null);
    }, [getColumnByName]);

    const getTargetTableName = useCallback((columnName: string) => {
        const column = getColumnByName(columnName);
        return column?.ReferencedTable ?? null;
    }, [getColumnByName]);

    // Entity search functionality
    const handleEntitySearch = useCallback((columnName: string, value: string) => {
        const targetTable = getTargetTableName(columnName);
        if (!targetTable) {
            toast.error(t('couldNotDetermineTargetTable'));
            return;
        }

        setEntitySearchData({
            columnName,
            value,
            targetTable
        });

        const targetTableRef = buildSourceObjectRef(item, current, schema, targetTable);
        if (!targetTableRef) {
            toast.error(t('couldNotDetermineTargetTable'));
            return;
        }

        // First, fetch the target table's column metadata to find its primary key
        void getColumns({
            variables: {
                ref: targetTableRef,
            },
        }).then(({ data, error }) => {
            if (error) {
                throw error;
            }

            // Find the primary key column in the target table
            const targetPrimaryKey = data?.Columns?.find(col => col.IsPrimary);

            if (!targetPrimaryKey) {
                const errorMessage = t('noPrimaryKeyFound').replace('{table}', targetTable);
                toast.error(errorMessage);
                return;
            }

            const primaryKeyName = targetPrimaryKey.Name;

            // Now search for the entity using the correct primary key
            void getStorageUnitRows({
                variables: {
                    ref: targetTableRef,
                    where: {
                        Type: WhereConditionType.Atomic,
                        Atomic: {
                            Key: primaryKeyName,
                            Operator: "=",
                            Value: value,
                            ColumnType: "string"
                        }
                    },
                    pageSize: 1,
                    pageOffset: 0
                },
            }).then(({ data, error }) => {
                if (error) {
                    throw error;
                }

                setEntitySearchResults(data?.Row ?? null);
                setShowEntitySearchSheet(true);
            }).catch((error: unknown) => {
                const errorMessage = t('failedToSearchEntity').replace(
                    '{error}',
                    error instanceof Error ? error.message : String((error as { message?: string }).message ?? '')
                );
                toast.error(errorMessage);
            });
        }).catch((error: unknown) => {
            const errorMessage = t('failedToGetTargetTableStructure').replace(
                '{error}',
                error instanceof Error ? error.message : String((error as { message?: string }).message ?? '')
            );
            toast.error(errorMessage);
        });
    }, [current, getColumns, getStorageUnitRows, getTargetTableName, item, schema, t]);

    const handleCloseEntitySearchSheet = useCallback(() => {
        setShowEntitySearchSheet(false);
        setEntitySearchData(null);
        setEntitySearchResults(null);
    }, []);

    if (unit == null) {
        return (
            <InternalPage
                routes={routes}
                className="relative"
                sidebar={<SchemaViewer parentRef={currentParentRef} selectedName={undefined} trail={browserTrail} />}
            >
                <LoadingPage />
            </InternalPage>
        );
    }

    if (isContentObject) {
        return (
            <InternalPage
                routes={routes}
                className="relative"
                sidebar={<SchemaViewer parentRef={currentParentRef} selectedName={unit.Name} trail={browserTrail} />}
            >
                <div className="flex flex-col grow gap-lg h-[calc(100%-100px)]">
                    <div className="flex items-center justify-between">
                        <div className="flex gap-sm items-center">
                            <h1 className="text-xl font-bold mr-4">{unitName}</h1>
                        </div>
                        {sourceContent?.MIMEType && (
                            <div className="text-sm text-muted-foreground">{sourceContent.MIMEType}</div>
                        )}
                    </div>
                    <StackList>
                        {unit.Attributes.map(attribute => {
                            const rawDisplay = formatAttributeValue(attribute.Key, attribute.Value);
                            const display = formatAttributeValue(attribute.Key, attribute.Value, t);
                            return (
                                <div key={attribute.Key} data-field-key={attribute.Key} data-field-value={rawDisplay}>
                                    <StackListItem item={formatAttributeKey(attribute.Key, t)}>
                                        {display}
                                    </StackListItem>
                                </div>
                            );
                        })}
                    </StackList>
                    {sourceContentLoading ? (
                        <div className="flex justify-center items-center grow">
                            <Loading />
                        </div>
                    ) : sourceContent?.Text ? (
                        sourceContentLanguage ? (
                            <div className={`grow min-h-[400px] border rounded-md overflow-hidden ${ph.noCapture}`}>
                                <CodeEditor
                                    language={sourceContentLanguage}
                                    value={sourceContent.Text}
                                    disabled={true}
                                />
                            </div>
                        ) : (
                            <div className={`grow min-h-[400px] overflow-auto rounded-md border bg-background/40 p-4 ${ph.mask}`}>
                                <pre className="whitespace-pre-wrap break-words text-sm">{sourceContent.Text}</pre>
                            </div>
                        )
                    ) : (
                        <div className="grow rounded-md border bg-background/40" />
                    )}
                </div>
            </InternalPage>
        );
    }

    return <InternalPage routes={routes} className="relative" sidebar={<SchemaViewer parentRef={currentParentRef} selectedName={unit.Name} trail={browserTrail} />}>
        <div className="ce-explore-content flex flex-col grow gap-lg h-[calc(100%-100px)]">
            <div className="ce-explore-heading flex items-center justify-between">
                <div className="flex gap-sm items-center">
                    <h1 className="text-xl font-bold">{unitName}</h1>
                    <span className="ce-explore-count" data-testid="total-count-top">{totalCount} {t('rows')}</span>
                </div>
                <div className="ce-explore-heading-actions" ref={setTableActionsTarget} />
            </div>
            <Sheet open={showAdd} onOpenChange={setShowAdd}>
                <SheetContent
                    side="right"
                    className="ce-add-row-sheet flex flex-col"
                    onKeyDown={(e) => {
                        if (e.key === 'Escape') {
                            e.preventDefault();
                            e.stopPropagation();
                            setShowAdd(false);
                        }
                    }}
                    footer={
                        <SheetFooter className="ce-add-row-footer">
                            <span className="ce-add-row-required-count">{t('requiredFields', {count: constraintsData?.SourceFieldConstraints?.filter(c => c.Nullable !== true && !c.Identity && c.DefaultValue == null).length ?? 0})}</span>
                            <Button
                                variant="secondary"
                                onClick={() =>{  setShowAdd(false); }}
                                data-testid="cancel-add-row"
                            >
                                {t('cancel')}
                            </Button>
                            <Button onClick={handleAddRowSubmit} data-testid="submit-add-row-button" disabled={adding}>
                                {adding && <Spinner className="size-4" aria-hidden="true" />} {t('addRowButton')}
                            </Button>
                        </SheetFooter>
                    }
                >
                    <div className="ce-add-row-header"><span>{unitName}</span><SheetTitle>{t('addRowTitle')}</SheetTitle></div>
                    <div className="ce-add-row-body flex-1 overflow-y-auto">
                        {/* NoSQL Document input - show JSON editor */}
                        {isNoSQL && rows?.Columns?.length === 1 && rows?.Columns?.[0]?.Type === "Document" ? (
                            <div className="flex flex-col gap-4" data-testid="add-row-field-document">
                                <Label>{t('documentJson')}</Label>
                                <p className="text-xs text-muted-foreground">{t('documentJsonHelp')}</p>
                                <div className={`h-[300px] border rounded-md overflow-hidden ${ph.noCapture}`}>
                                    <CodeEditor
                                        language="json"
                                        value={addRowData.document ?? "{\n  \n}"}
                                        setValue={(value) =>{  handleAddRowFieldChange("document", value); }}
                                    />
                                </div>
                            </div>
                        ) : (
                            /* Regular column-based input for SQL databases */
                            <div className="flex flex-col gap-4">
                                {rows?.Columns?.map((col) => {
                                    const constraint = constraintsData?.SourceFieldConstraints?.find(c => c.Name === col.Name);
                                    const isRequired = constraint ? (constraint.Nullable !== true && !constraint.Identity && constraint.DefaultValue == null) : false;
                                    const isIdentity = constraint?.Identity === true;
                                    const hasDefault = constraint?.DefaultValue != null;
                                    return (
                                        <div key={col.Name} className="ce-add-row-field flex flex-col gap-2"
                                             data-testid={`add-row-field-${col.Name}`}>
                                            <div className="ce-add-row-field-heading">
                                                <div className="ce-add-row-field-name">
                                                    <Label className="w-fit" htmlFor={`add-row-${col.Name}`}>
                                                        {col.Name}
                                                    </Label>
                                                    {isRequired && <Badge variant="secondary" className="text-[10px] px-1 py-0">{t('required')}</Badge>}
                                                    {isIdentity && <Badge variant="outline" className="text-[10px] px-1 py-0">{t('autoGenerated')}</Badge>}
                                                    {hasDefault && !isIdentity && <Badge variant="outline" className="text-[10px] px-1 py-0">{t('hasDefault')}</Badge>}
                                                </div>
                                                <span>{col.IsForeignKey && col.ReferencedTable && col.ReferencedColumn ? `→ ${col.ReferencedTable}.${col.ReferencedColumn}` : col.Type?.toLowerCase()}</span>
                                            </div>
                                            {col.IsForeignKey && col.ReferencedTable && col.ReferencedColumn && current ? (
                                                <ForeignKeyAddRowField
                                                    reference={buildSourceObjectRef(item, current, schema, col.ReferencedTable)}
                                                    column={col.ReferencedColumn}
                                                    fieldName={col.Name}
                                                    value={addRowData[col.Name] ?? ""}
                                                    onChange={value => { handleAddRowFieldChange(col.Name, value); }}
                                                />
                                            ) : <Input
                                                id={`add-row-${col.Name}`}
                                                value={addRowData[col.Name] ?? ""}
                                                onChange={e =>{  handleAddRowFieldChange(col.Name, e.target.value); }}
                                                placeholder={hasDefault ? t('defaultValuePlaceholder', {value: constraint?.DefaultValue ?? ''}) : isIdentity ? t('autoGeneratedPlaceholder') : t('enterValueFor', {column: col.Name})}
                                                disabled={isIdentity}
                                                {...getInputPropsForColumnType(col.Type || '')}
                                            />}
                                        </div>
                                    );
                                })}
                            </div>
                        )}
                        {rows?.Columns?.some(col => col.IsForeignKey && col.ReferencedTable) && <p className="ce-add-row-fk-note">{t('foreignKeyPickerHelp')}</p>}
                        {addRowError && (
                            <ErrorState error={addRowError} />
                        )}
                    </div>
                </SheetContent>
            </Sheet>
            {supportsScratchpad && (
                <div className="flex flex-col gap-2 w-full" data-testid="inline-sql-console">
                    <div className="ce-inline-sql-bar flex items-center gap-2 w-full rounded-lg border border-border bg-card/60 px-3 py-1.5">
                        <CommandLineIcon className="w-4 h-4 text-muted-foreground shrink-0" />
                        <div className={`ce-inline-sql-editor flex-1 min-w-0 ${ph.noCapture}`}>
                            <CodeEditor
                                language="sql"
                                value={code}
                                setValue={handleCodeChange}
                                onRun={handleScratchpad}
                                sqlSchema={sqlSchema}
                            />
                        </div>
                        <div className="flex items-center gap-1.5 shrink-0">
                            {activeSqlQuery != null && (
                                <>
                                    <Badge variant="secondary" className="text-[11px] px-2 py-0.5 whitespace-nowrap">
                                        {t('sqlResultBadge')}
                                    </Badge>
                                    <Button
                                        variant="ghost"
                                        size="sm"
                                        className="h-7 px-2 text-xs gap-1"
                                        onClick={handleResetSqlResult}
                                        data-testid="reset-sql-result-button"
                                    >
                                        <ArrowPathIcon className="w-3.5 h-3.5" />
                                        {t('resetTableData')}
                                    </Button>
                                </>
                            )}
                            <Button
                                variant="outline"
                                size="sm"
                                className="h-7 px-2.5 text-xs gap-1.5"
                                onClick={() => { setIsSqlHistoryOpen(true); }}
                                data-testid="sql-history-button"
                            >
                                <ClockIcon className="w-3.5 h-3.5" />
                                {t('sqlHistory')}
                                {sqlHistory.length > 0 && (
                                    <Badge variant="secondary" className="text-[10px] px-1.5 py-0 h-4 min-w-4 flex items-center justify-center">
                                        {sqlHistory.length}
                                    </Badge>
                                )}
                            </Button>
                            <Button
                                size="sm"
                                className="h-7 px-3 text-xs gap-1.5"
                                onClick={() => { handleScratchpad(); }}
                                disabled={!code.trim() || rawExecuteLoading}
                                data-testid="run-submit-button"
                            >
                                {rawExecuteLoading ? <Spinner className="size-3.5" /> : <WhoDBChatIcon name="play" />}
                                {t('executeSql')}
                            </Button>
                        </div>
                    </div>
                    {sqlError != null && (
                        <div className="relative">
                            <ErrorState error={sqlError} />
                            <Button
                                variant="ghost"
                                size="icon"
                                className="absolute top-2 right-2 h-6 w-6"
                                onClick={() => { setSqlError(null); }}
                                aria-label={t('close')}
                            >
                                <XMarkIcon className="w-3.5 h-3.5" />
                            </Button>
                        </div>
                    )}
                </div>
            )}
            <div className="grow" ref={tableContainerRef}>
                {(loading || rawExecuteLoading) && rows == null ? (
                    <div className="ce-explore-loading-grid" data-testid="explore-rows-loading">
                        <div className="ce-explore-loading-row ce-explore-loading-header">
                            <span className="ce-explore-loading-check" />
                            {Array.from({length: 5}, (_, index) => <span key={index} className="ce-explore-loading-cell"><span /></span>)}
                        </div>
                        {Array.from({length: 6}, (_, rowIndex) => <div key={rowIndex} className="ce-explore-loading-row">
                            <span className="ce-explore-loading-check" />
                            {Array.from({length: 5}, (_, cellIndex) => <span key={cellIndex} className="ce-explore-loading-cell"><span /></span>)}
                        </div>)}
                        <div className="ce-explore-loading-badge">
                            <Loading size="sm" className="size-4" />
                            <span>{t('loadingRows')}</span>
                        </div>
                    </div>
                ) : rows != null ? (
                    <StorageUnitTable
                        columns={columns}
                        rows={displayedRows}
                        onRowUpdate={handleRowUpdate}
                        cellEditing
                        columnTypes={columnTypes}
                        columnComments={columnComments}
                        columnIsPrimary={columnIsPrimary}
                        columnIsForeignKey={columnIsForeignKey}
                        schema={schema}
                        storageUnit={unitName}
                        sourceLabel={current?.Database}
                        objectRef={currentUnitRef}
                        rawQuery={activeSqlQuery ?? undefined}
                        onRefresh={handleTableRefresh}
                        onColumnSort={handleColumnSort}
                        sortedColumns={sortedColumnsMap}
                        searchRef={searchRef}
                        pageSize={pageSize}
                        height={tableHeight}
                        // Server-side pagination props
                        totalCount={Number.parseInt(totalCount, 10)}
                        currentPage={currentPage}
                        onPageChange={handlePageChange}
                        showPagination={true}
                        actionsTarget={tableActionsTarget}
                        // Foreign key functionality
                        isValidForeignKey={isValidForeignKey}
                        onEntitySearch={handleEntitySearch}
                        databaseType={current?.Type}
                        allowRowUpdate={allowsUpdateData && activeSqlQuery == null && !rows.DisableUpdate}
                        allowRowDelete={allowsDeleteData && activeSqlQuery == null && !rows.DisableUpdate}
                        // Mock data control - disabled for views/materialized views
                        isMockDataGenerationAllowed={isMockDataGenerationAllowed}
                        // Import control - enabled for explore view
                        allowImport={true}
                        enableKeyboardShortcuts={true}
                        formatDatesLocale={formatDatesLocale}
                        formatBooleansReadable={formatBooleansReadable}
                    >
                        {allowsInsertData && <div className="flex gap-2">
                            <Button onClick={handleOpenAddSheet} disabled={adding} data-testid="add-row-button">
                                <WhoDBChatIcon name="plus" /> {t('addRowButton')}
                            </Button>
                        </div>}
                    </StorageUnitTable>
                ) : null}
            </div>
        </div>
        <Sheet open={isSqlHistoryOpen} onOpenChange={setIsSqlHistoryOpen}>
            <SheetContent side="right" className="w-[min(640px,100vw)] sm:max-w-[min(640px,100vw)] p-0 flex flex-col" data-testid="sql-history-sheet">
                <div className="flex items-center justify-between px-6 py-4 border-b border-border">
                    <SheetTitle className="flex items-center gap-2 text-base font-semibold">
                        <ClockIcon className="w-5 h-5" />
                        {t('sqlHistoryTitle')}
                    </SheetTitle>
                </div>
                <div className="flex-1 px-6 py-4 overflow-y-auto">
                    {sqlHistory.length === 0 ? (
                        <EmptyState
                            title={t('noSqlHistoryTitle')}
                            description={t('noSqlHistoryDescription')}
                            icon={<ClockIcon className="w-10 h-10" />}
                        />
                    ) : (
                        <div className="flex flex-col gap-3">
                            {sqlHistory.map(historyItem => {
                                const parsedDate = new Date(historyItem.timestamp);
                                const formattedTime = Number.isNaN(parsedDate.getTime())
                                    ? historyItem.timestamp
                                    : formatDate(parsedDate);
                                return (
                                    <Card className="w-full p-4 relative" key={historyItem.id}>
                                        <div className="flex flex-col gap-2">
                                            <div className="flex items-center justify-between gap-2 flex-wrap">
                                                <div className="flex items-center gap-1.5 flex-wrap">
                                                    <Badge
                                                        variant={historyItem.status ? "default" : "destructive"}
                                                        className={cn("text-[11px] px-2 py-0", {
                                                            "!bg-green-600 !text-white": historyItem.status,
                                                            "!bg-red-600 !text-white": !historyItem.status,
                                                        })}
                                                    >
                                                        {historyItem.status ? t('sqlStatusSuccess') : t('sqlStatusError')}
                                                    </Badge>
                                                    {historyItem.table && (
                                                        <Badge variant="outline" className="text-[11px] px-2 py-0">
                                                            {historyItem.table}
                                                        </Badge>
                                                    )}
                                                    {historyItem.rowCount != null && (
                                                        <Badge variant="secondary" className="text-[11px] px-2 py-0">
                                                            {historyItem.rowCount} {t('rows')}
                                                        </Badge>
                                                    )}
                                                </div>
                                                <span className="text-xs text-muted-foreground">
                                                    {formattedTime}
                                                </span>
                                            </div>
                                            <div
                                                className="rounded-md overflow-hidden bg-muted/60 dark:bg-muted/30 cursor-pointer hover:bg-muted transition-colors"
                                                onClick={() => {
                                                    handleCodeChange(historyItem.sql);
                                                    setIsSqlHistoryOpen(false);
                                                }}
                                                title={t('fillToEditor')}
                                            >
                                                <pre className="text-xs font-mono p-3 whitespace-pre-wrap break-all">
                                                    <code>{historyItem.sql}</code>
                                                </pre>
                                            </div>
                                            <div className="flex justify-end items-center gap-1.5 mt-1">
                                                <CopyButton text={historyItem.sql} />
                                                <Tip className="w-fit">
                                                    <Button
                                                        size="icon"
                                                        variant="secondary"
                                                        className="h-7 w-7 border border-input"
                                                        onClick={() => {
                                                            handleCodeChange(historyItem.sql);
                                                            setIsSqlHistoryOpen(false);
                                                        }}
                                                        aria-label={t('fillToEditor')}
                                                        data-testid="fill-sql-history-button"
                                                    >
                                                        <PencilIcon className="w-3.5 h-3.5" />
                                                    </Button>
                                                    <p>{t('fillToEditor')}</p>
                                                </Tip>
                                                <Tip className="w-fit">
                                                    <Button
                                                        size="icon"
                                                        variant="secondary"
                                                        className="h-7 w-7 border border-input"
                                                        onClick={() => {
                                                            handleCodeChange(historyItem.sql);
                                                            setIsSqlHistoryOpen(false);
                                                            handleScratchpad(historyItem.sql);
                                                        }}
                                                        aria-label={t('runImmediately')}
                                                        data-testid="run-sql-history-button"
                                                    >
                                                        <PlayIcon className="w-3.5 h-3.5" />
                                                    </Button>
                                                    <p>{t('runImmediately')}</p>
                                                </Tip>
                                                <Tip className="w-fit">
                                                    <Button
                                                        size="icon"
                                                        variant="ghost"
                                                        className="h-7 w-7 text-muted-foreground hover:text-destructive"
                                                        onClick={() => {
                                                            dispatch(ExploreConditionsActions.removeSqlHistoryItem({ id: historyItem.id }));
                                                        }}
                                                        aria-label={t('deleteHistoryItem')}
                                                        data-testid="delete-sql-history-button"
                                                    >
                                                        <TrashIcon className="w-3.5 h-3.5" />
                                                    </Button>
                                                    <p>{t('deleteHistoryItem')}</p>
                                                </Tip>
                                            </div>
                                        </div>
                                    </Card>
                                );
                            })}
                        </div>
                    )}
                </div>
                {sqlHistory.length > 0 && (
                    <SheetFooter className="px-6 py-3 border-t border-border">
                        <AlertDialog>
                            <AlertDialogTrigger>
                                <Button
                                    variant="outline"
                                    size="sm"
                                    data-testid="clear-sql-history-button"
                                    className="gap-1.5"
                                >
                                    <TrashIcon className="w-3.5 h-3.5" />
                                    {t('clearSqlHistory')}
                                </Button>
                            </AlertDialogTrigger>
                            <AlertDialogContent>
                                <AlertDialogHeader>
                                    <AlertDialogTitle>{t('clearSqlHistoryTitle')}</AlertDialogTitle>
                                    <AlertDialogDescription>{t('clearSqlHistoryDescription')}</AlertDialogDescription>
                                </AlertDialogHeader>
                                <AlertDialogFooter>
                                    <AlertDialogCancel>{t('cancel')}</AlertDialogCancel>
                                    <AlertDialogAction asChild>
                                        <Button
                                            variant="destructive"
                                            onClick={() => {
                                                dispatch(ExploreConditionsActions.clearSqlHistory({
                                                    table: unitName,
                                                    database: current?.Database ?? schema,
                                                }));
                                            }}
                                            data-testid="clear-sql-history-confirm"
                                        >
                                            {t('clearSqlHistory')}
                                        </Button>
                                    </AlertDialogAction>
                                </AlertDialogFooter>
                            </AlertDialogContent>
                        </AlertDialog>
                    </SheetFooter>
                )}
            </SheetContent>
        </Sheet>
        <AlertDialog open={pendingScratchpadQuery != null} onOpenChange={(open) => { if (!open) setPendingScratchpadQuery(null); }}>
            <AlertDialogContent>
                <AlertDialogHeader>
                    <AlertDialogTitle>{t('confirmOperationTitle')}</AlertDialogTitle>
                    <AlertDialogDescription>{t('confirmOperationDescription')}</AlertDialogDescription>
                </AlertDialogHeader>
                <AlertDialogFooter>
                    <AlertDialogCancel data-testid="execute-query-cancel" onClick={() => { setPendingScratchpadQuery(null); }}>{t('cancel')}</AlertDialogCancel>
                    <AlertDialogAction asChild>
                        <Button variant="destructive" data-testid="execute-query-confirm" onClick={() => {
                            if (pendingScratchpadQuery != null) {
                                doScratchpadExecute(pendingScratchpadQuery);
                                setPendingScratchpadQuery(null);
                            }
                        }}>
                            {t('run')}
                        </Button>
                    </AlertDialogAction>
                </AlertDialogFooter>
            </AlertDialogContent>
        </AlertDialog>
        <Sheet open={showEntitySearchSheet} onOpenChange={setShowEntitySearchSheet}>
            <SheetContent side="right" className="flex flex-col p-8 min-w-[600px]" footer={
                <SheetFooter>
                    <Button onClick={handleCloseEntitySearchSheet} variant="outline">
                        {t('close')}
                    </Button>
                </SheetFooter>
            }>
                <div className="text-lg font-semibold mb-4 flex items-center gap-2">
                    <WhoDBChatIcon name="search" className="w-5 h-5" />
                    {t('searchAround')}
                </div>
                {entitySearchData && (
                    <div className="text-sm text-gray-500">
                        {t('entitySearchQuery', { table: entitySearchData.targetTable, id: entitySearchData.value })}
                    </div>
                )}
                <div className="flex-1 overflow-y-auto pr-2">
                    {entitySearchResults && entitySearchResults.Rows.length > 0 ? (
                        <div className="flex flex-col gap-4">
                            <StackList>
                                {entitySearchData && (
                                    <StackListItem key="entity-id" item="ID">
                                        {entitySearchData.value}
                                    </StackListItem>
                                )}
                                {entitySearchResults.Columns.map((column, index) => (
                                    <StackListItem 
                                        key={column.Name} 
                                        item={
                                            isValidForeignKey(column.Name) ? (
                                                <Badge className="text-lg" data-testid="foreign-key-attribute">
                                                    {column.Name}
                                                </Badge>
                                            ) : column.Name
                                        }
                                    >
                                        {entitySearchResults.Rows[0][index]}
                                    </StackListItem>
                                ))}
                            </StackList>
                        </div>
                    ) : (
                        <div className="text-center text-gray-500 py-8">
                            {t('noEntityFound')}
                        </div>
                    )}
                </div>
            </SheetContent>
        </Sheet>
    </InternalPage>
}
