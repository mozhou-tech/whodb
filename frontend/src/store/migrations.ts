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

import { featureFlags } from '../config/features';
import { withBasePath } from '../utils/base-path';
import { stripProfileSecrets } from '../utils/credential-secrets';
import { isDesktopApp } from '../utils/external-links';

/**
 * Get the current migration version
 */
function getMigrationVersion(): number {
  const version = localStorage.getItem('whodb_migration_version');
  return version ? parseInt(version, 10) : 0;
}

/**
 * Set the migration version
 */
function setMigrationVersion(version: number): void {
  localStorage.setItem('whodb_migration_version', version.toString());
}

/**
 * Clear legacy persisted chat and AI model state.
 */
function clearLegacyChatState(): void {
  try {
    localStorage.removeItem('persist:houdini');
    localStorage.removeItem('persist:aiModels');
  } catch (error) {
    console.error('Error clearing legacy chat state:', error);
  }
}

/**
 * Apply extension settings defaults
 */
function applyEESettingsDefaultsV4(): void {
  try {
    // Only run when settings page is hidden (extended mode)
    if (featureFlags.settingsPage) {
      return;
    }

    const persistedSettingsState = localStorage.getItem('persist:settings');
    if (!persistedSettingsState) {
      return;
    }

    const settingsState = JSON.parse(persistedSettingsState);
    let needsUpdate = false;

    // Update whereConditionMode to extension default ('sheet')
    if (settingsState.whereConditionMode) {
      try {
        const whereConditionMode = JSON.parse(settingsState.whereConditionMode);
        if (whereConditionMode !== 'sheet') {
          settingsState.whereConditionMode = JSON.stringify('sheet');
          needsUpdate = true;
        }
      } catch {
        // If parsing fails, set to extension default
        settingsState.whereConditionMode = JSON.stringify('sheet');
        needsUpdate = true;
      }
    } else {
      // Initialize with extension default if missing
      settingsState.whereConditionMode = JSON.stringify('sheet');
      needsUpdate = true;
    }

    // Update disableAnimations to extension default (true)
    if (settingsState.disableAnimations) {
      try {
        const disableAnimations = JSON.parse(settingsState.disableAnimations);
        if (disableAnimations !== true) {
          settingsState.disableAnimations = JSON.stringify(true);
          needsUpdate = true;
        }
      } catch {
        // If parsing fails, set to extension default
        settingsState.disableAnimations = JSON.stringify(true);
        needsUpdate = true;
      }
    } else {
      // Initialize with extension default if missing
      settingsState.disableAnimations = JSON.stringify(true);
      needsUpdate = true;
    }

    if (needsUpdate) {
      localStorage.setItem('persist:settings', JSON.stringify(settingsState));
    }
  } catch (error) {
    console.error('Error applying settings defaults:', error);
  }
}

/**
 * Migrate language codes from old format (en, es, fr, de) to regional variants
 * (en_US, es_ES, fr_FR, de_DE).
 */
function migrateLanguageCodesV5(): void {
  try {
    const persistedSettingsState = localStorage.getItem('persist:settings');
    if (!persistedSettingsState) {
      return;
    }

    const settingsState = JSON.parse(persistedSettingsState);
    if (!settingsState.language) {
      return;
    }

    const languageMap: Record<string, string> = {
      'en': 'en_US',
      'es': 'es_ES',
      'fr': 'fr_FR',
      'de': 'de_DE',
    };

    const currentLanguage = JSON.parse(settingsState.language);
    const mapped = languageMap[currentLanguage];
    if (mapped) {
      settingsState.language = JSON.stringify(mapped);
      localStorage.setItem('persist:settings', JSON.stringify(settingsState));
    }
  } catch (error) {
    console.error('Error migrating language codes:', error);
  }
}

/**
 * Remove the orphaned persisted database metadata state now that metadata is
 * owned by Apollo session state instead of Redux persistence.
 */
function clearDatabaseMetadataStateV6(): void {
  try {
    localStorage.removeItem('persist:databaseMetadata');
  } catch (error) {
    console.error('Error clearing database metadata state:', error);
  }
}

/**
 * Clear persisted auth state again after the source-first Authorization header
 * contract corrections so stale logged-in sessions from earlier source-refactor
 * builds do not survive.
 */
function clearAuthStateV8(): void {
  try {
    localStorage.removeItem('persist:auth');
  } catch (error) {
    console.error('Error clearing auth state:', error);
  }
}

const PASSWORD_KEY = 'password';

/**
 * Migrate an already-logged-in browser user to the server-side session cookie.
 * If persisted credentials carry a password and no session cookie exists yet,
 * this posts LoginSource once (awaited, so it completes before the app renders
 * and any query races it), then rewrites persist:auth with all secrets stripped
 * — keeping the connection list and logged-in state. Best-effort: on failure it
 * simply leaves the user to log in again. Skipped on desktop/webview.
 */
export async function migrateAuthSessionCookieV9(): Promise<void> {
  try {
    if (typeof window === 'undefined' || getMigrationVersion() >= 9) {
      return;
    }
    // Desktop/webview keeps the Authorization-header flow; do not migrate.
    const wailsGo = (window as any).go;
    const hasWailsBindings = Boolean(wailsGo?.main?.App) || Boolean(wailsGo?.common?.App);
    const isDesktop = hasWailsBindings
      || !['http:', 'https:'].includes(window.location.protocol);

    const raw = localStorage.getItem('persist:auth');
    const parsed = raw ? JSON.parse(raw) : null;
    const current = parsed?.current ? JSON.parse(parsed.current) : null;
    const values: Array<{ Key: string; Value: string }> = current?.Values ?? [];
    const hasPassword = Array.isArray(values)
      && values.some(v => String(v?.Key ?? '').toLowerCase() === PASSWORD_KEY && v?.Value);

    if (!isDesktop && current && hasPassword) {
      const res = await fetch(withBasePath('/api/query'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          operationName: 'LoginSource',
          query: 'mutation LoginSource($credentials: SourceLoginInput!) { LoginSource(credentials: $credentials) { Status } }',
          variables: {
            credentials: {
              Id: current.Id,
              SourceType: current.SourceType ?? current.Type,
              Values: values,
              AccessToken: current.AccessToken,
            },
          },
        }),
      });
      // Only strip secrets once the cookie is established, so a failed mint
      // leaves the credentials intact for a manual retry/login.
      if (res.ok && parsed) {
        if (parsed.current) parsed.current = JSON.stringify(stripProfileSecrets(current));
        if (parsed.profiles) {
          const profiles = JSON.parse(parsed.profiles);
          parsed.profiles = JSON.stringify(
            Array.isArray(profiles) ? profiles.map(stripProfileSecrets) : profiles
          );
        }
        localStorage.setItem('persist:auth', JSON.stringify(parsed));
      }
    }
  } catch (error) {
    console.error('Error migrating auth to session cookie:', error);
  } finally {
    setMigrationVersion(9);
  }
}

/**
 * Removes the legacy Redux auth cache once browser sessions are restored from
 * the backend. Auth state is now in-memory only and is hydrated at startup
 * from the HttpOnly session cookie.
 */
export function clearPersistedAuthStateV10(): void {
  try {
    if (typeof window === 'undefined' || getMigrationVersion() >= 10) {
      return;
    }
    if (isDesktopApp()) {
      return;
    }
    localStorage.removeItem('persist:auth');
  } catch (error) {
    console.error('Error clearing persisted auth state:', error);
  } finally {
    setMigrationVersion(10);
  }
}

/**
 * Run all necessary migrations
 */
export function runMigrations(): void {
  const currentVersion = getMigrationVersion();

  clearLegacyChatState();

  if (currentVersion < 4) {
    applyEESettingsDefaultsV4();
    setMigrationVersion(4);
  }

  if (currentVersion < 5) {
    migrateLanguageCodesV5();
    setMigrationVersion(5);
  }

  if (currentVersion < 6) {
    clearDatabaseMetadataStateV6();
    setMigrationVersion(6);
  }

  if (currentVersion < 8) {
    clearAuthStateV8();
    setMigrationVersion(8);
  }

  // V9 (auth → session cookie) and V10 (remove persisted auth) run separately
  // before the app renders so the cookie beats any query.
}
