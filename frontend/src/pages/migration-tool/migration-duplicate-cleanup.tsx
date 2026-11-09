import {useLazyQuery, useMutation} from '@apollo/client/react';
import {Button, Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle, toast} from '@clidey/ux';
import {SourceCleanMigrationDuplicatesDocument, SourceDuplicateRetentionStrategy, SourceMigrationDuplicateCleanupPreviewDocument, type SourceMigrationDuplicateCleanupPreviewQuery, type SourceMigrationInput} from '@graphql';
import {useState} from 'react';
import {useTranslation} from '../../hooks/use-translation';

/** Previews and confirms primary-key based duplicate cleanup for the frozen migration target. */
export function MigrationDuplicateCleanup({input, version, object, onCleaned}: {input: SourceMigrationInput; version: string; object: string; onCleaned: () => void}) {
    const {t} = useTranslation('pages/migration-tool');
    const [strategy, setStrategy] = useState<SourceDuplicateRetentionStrategy | null>(null);
    const [preview, setPreview] = useState<SourceMigrationDuplicateCleanupPreviewQuery['SourceMigrationDuplicateCleanupPreview'] | null>(null);
    const [error, setError] = useState<string | null>(null);
    const [fetchPreview, {loading: previewing}] = useLazyQuery(SourceMigrationDuplicateCleanupPreviewDocument, {fetchPolicy: 'no-cache'});
    const [clean, {loading: cleaning}] = useMutation(SourceCleanMigrationDuplicatesDocument);
    const label = strategy === SourceDuplicateRetentionStrategy.Last ? t('keepLastDuplicate') : t('keepFirstDuplicate');
    const openPreview = async (next: SourceDuplicateRetentionStrategy) => {
        setStrategy(next);setPreview(null);setError(null);
        try {
            const result = await fetchPreview({variables: {input, version, object, strategy: next}});
            if (result.error) throw result.error;
            if (!result.data) throw new Error(t('duplicateExportNoResult'));
            setPreview(result.data.SourceMigrationDuplicateCleanupPreview);
        } catch (err) {setError(t('duplicateCleanupFailed', {error: err instanceof Error ? err.message : String(err)}));}
    };
    const apply = async () => {
        if (!preview || !strategy) return;
        setError(null);
        try {
            const result = await clean({variables: {input, version, object, strategy, fingerprint: preview.Fingerprint}});
            if (!result.data) throw new Error(t('duplicateExportNoResult'));
            toast.success(t('duplicateCleanupSuccess', {count: result.data.SourceCleanMigrationDuplicates.Deleted}));
            setStrategy(null);onCleaned();
        } catch (err) {
            setError(t('duplicateCleanupFailed', {error: err instanceof Error ? err.message : String(err)}));
            setPreview(null);
        }
    };
    return <>
        <Button size="sm" variant="outline" disabled={strategy !== null} onClick={() => {void openPreview(SourceDuplicateRetentionStrategy.Last);}} data-testid="keep-last-duplicate">{t('keepLastDuplicate')}</Button>
        <Button size="sm" variant="outline" disabled={strategy !== null} onClick={() => {void openPreview(SourceDuplicateRetentionStrategy.First);}} data-testid="keep-first-duplicate">{t('keepFirstDuplicate')}</Button>
        <Dialog open={strategy !== null} onOpenChange={open => {if (!open && !cleaning && !previewing) setStrategy(null);}}>
            <DialogContent data-testid="duplicate-cleanup-dialog">
                <DialogHeader><DialogTitle>{label}</DialogTitle><DialogDescription>{t('duplicateCleanupTarget', {database: input.Target.Database ?? '', object})}</DialogDescription></DialogHeader>
                {previewing && <p>{t('duplicateCleanupPreviewing')}</p>}
                {preview && <div className="space-y-3 text-sm">
                    <p>{t('duplicateCleanupOrder', {keys: preview.PrimaryKeyColumns.join(', '), strategy: label})}</p>
                    <p>{t('duplicateCleanupCounts', {groups: preview.Groups, count: preview.RowsToDelete})}</p>
                    <p className="text-destructive">{t('duplicateCleanupWarning')}</p>
                </div>}
                {error && <p role="alert" className="text-sm text-destructive">{error}</p>}
                <DialogFooter>
                    <Button variant="outline" disabled={cleaning || previewing} onClick={() => {setStrategy(null);}}>{t('duplicateCleanupCancel')}</Button>
                    <Button variant="destructive" disabled={!preview || preview.RowsToDelete === 0 || cleaning || previewing} onClick={() => {void apply();}} data-testid="confirm-duplicate-cleanup">{cleaning ? t('duplicateCleanupRunning') : t('duplicateCleanupConfirm')}</Button>
                </DialogFooter>
            </DialogContent>
        </Dialog>
    </>;
}
