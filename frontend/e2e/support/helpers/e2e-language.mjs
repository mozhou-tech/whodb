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

/**
 * Pins Playwright to English before the app boots.
 *
 * The product default is zh_CN. These tests assert English labels, so a fresh
 * profile needs en_US in redux-persist before the first script runs. An
 * existing saved language is left alone.
 *
 * @param {import('@playwright/test').Page} page
 */
export async function pinE2ELanguage(page) {
    await page.addInitScript(() => {
        const key = "persist:settings";
        let settings = {};
        try {
            settings = JSON.parse(localStorage.getItem(key) || "{}");
        } catch {
            settings = {};
        }
        if (!settings.language) {
            settings.language = JSON.stringify("en_US");
            localStorage.setItem(key, JSON.stringify(settings));
        }
    });
}
