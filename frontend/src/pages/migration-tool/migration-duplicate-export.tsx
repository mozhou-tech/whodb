import {useLazyQuery} from '@apollo/client/react';
import {Button, IconDownload, toast} from '@clidey/ux';
import {SourceMigrationDuplicateRowsDocument, type SourceMigrationInput} from '@graphql';
import {useState} from 'react';
import {useExportToCSV} from '../../components/hooks';
import {useTranslation} from '../../hooks/use-translation';

/** Exports complete duplicate records from the diagnosed target through the existing Excel download flow. */
export function MigrationDuplicateExport({input, version, object}: {input: SourceMigrationInput; version: string; object: string}) {
    const {t} = useTranslation('pages/migration-tool');
    const [exporting, setExporting] = useState(false);
    const [fetchRows] = useLazyQuery(SourceMigrationDuplicateRowsDocument, {fetchPolicy: 'no-cache'});
    const filename = t('duplicateExportFilename', {database: input.Target.Database ?? '', object}).replace(/[\\/:*?"<>|]/g, '_');
    const exportRows = useExportToCSV(undefined, filename, true, ',', undefined, 'excel');
    const handleExport = async () => {
        setExporting(true);
        try {
            const result = await fetchRows({variables: {input, version, object}});
            if (result.error) throw result.error;
            const rows = result.data?.SourceMigrationDuplicateRows;
            if (!rows) throw new Error(t('duplicateExportNoResult'));
            if (rows.Rows.length === 0) {
                toast.info(t('duplicateExportEmpty'));
                return;
            }
            await exportRows(rows.Rows.map(row => Object.fromEntries(rows.Columns.map((column, index) => [column.Name, row[index]]))));
        } catch (error) {
            toast.error(t('duplicateExportFailed', {error: error instanceof Error ? error.message : String(error)}));
        } finally {
            setExporting(false);
        }
    };
    return <Button variant="outline" size="sm" disabled={exporting} onClick={() => {void handleExport();}} data-testid="export-migration-duplicates">
        <IconDownload className="size-4" />
        {exporting ? t('exportingDuplicates') : t('exportDuplicates')}
    </Button>;
}
