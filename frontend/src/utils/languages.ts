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

export type SupportedLanguage =
    | 'af_ZA'
    | 'am_ET'
    | 'ar_AE'
    | 'az_AZ'
    | 'bg_BG'
    | 'bn_BD'
    | 'ca_ES'
    | 'cs_CZ'
    | 'da_DK'
    | 'de_DE'
    | 'el_GR'
    | 'en_GB'
    | 'en_US'
    | 'es_ES'
    | 'et_EE'
    | 'fa_IR'
    | 'fi_FI'
    | 'fr_FR'
    | 'gu_IN'
    | 'ha_NG'
    | 'hi_IN'
    | 'hr_HR'
    | 'hu_HU'
    | 'id_ID'
    | 'it_IT'
    | 'iw_IL'
    | 'ja_JP'
    | 'ka_GE'
    | 'km_KH'
    | 'kn_IN'
    | 'ko_KR'
    | 'lt_LT'
    | 'lv_LV'
    | 'ml_IN'
    | 'mr_IN'
    | 'ms_MY'
    | 'nb_NO'
    | 'ne_NP'
    | 'nl_NL'
    | 'pa_IN'
    | 'pl_PL'
    | 'pt_BR'
    // | 'pt_PT'  // TODO: Google Translate doesn't differentiate PT-BR vs PT-PT — add back when we have distinct translations
    | 'ro_RO'
    | 'ru_RU'
    | 'si_LK'
    | 'sk_SK'
    | 'sl_SI'
    | 'sr_RS'
    | 'sv_SE'
    | 'sw_KE'
    | 'ta_IN'
    | 'te_IN'
    | 'th_TH'
    | 'tl_PH'
    | 'tr_TR'
    | 'uk_UA'
    | 'ur_PK'
    | 'uz_UZ'
    | 'vi_VN'
    | 'yo_NG'
    | 'zh_CN'
    // | 'zh_HK'  // TODO: Google Translate has no zh-HK variant (produces same as zh-TW) — add back when we have distinct translations
    | 'zh_TW';

/** Language shown when the user has not chosen one. */
export const DEFAULT_LANGUAGE: SupportedLanguage = 'zh_CN';

/**
 * Bundled translations and the fallback for a missing key.
 */
export const FALLBACK_LANGUAGE: SupportedLanguage = 'zh_CN';

export const SUPPORTED_LANGUAGES: Record<SupportedLanguage, string> = {
    zh_CN: '中文 (简体)',
} as Record<SupportedLanguage, string>;

export function isSupportedLanguage(value: string): value is SupportedLanguage {
    return value === 'zh_CN';
}
