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

import { Button, cn, Kbd, SidebarProvider, Tooltip, TooltipContent, TooltipTrigger, useTheme, IconMenu as Bars3Icon } from '@clidey/ux';
import classNames from "classnames";
import type {CSSProperties, FC, MouseEvent as ReactMouseEvent, ReactNode} from "react";
import {useCallback, useEffect, useRef, useState} from "react";
import {twMerge} from "tailwind-merge";
import {InternalRoutes, type IInternalRoute} from "../config/routes";
import {useAppDispatch, useAppSelector} from "../store/hooks";
import {SettingsActions} from "../store/settings";
import {Breadcrumb} from "./breadcrumbs";
import {ConnectionContext} from "./connection-context";
import {Loading} from "./loading";
import {Sidebar} from "./sidebar/sidebar";
import {useTranslation} from "@/hooks/use-translation";
import {WhoDBChatIcon} from "./whodb-chat-icon";
import {isMacPlatform} from "@/utils/platform";
import {useSourceSessionMetadata} from "@/hooks/useSourceSessionMetadata";

type IPageProps = {
    wrapperClassName?: string;
    className?: string;
    children: ReactNode;
}

export const Page: FC<IPageProps> = (props) => {
    return <div className={twMerge("flex grow min-w-0 px-8 py-6 flex-col h-full w-full overflow-hidden", props.wrapperClassName)}>
        <div className={twMerge("flex flex-row grow flex-wrap gap-sm w-full h-full overflow-y-auto overscroll-none", props.className)}
            data-testid="page-scroll-container">
                {props.children}
        </div>
    </div>
}

type IInternalPageProps = IPageProps & {
    sidebar?: ReactNode;
    children: ReactNode;
    routes?: IInternalRoute[];
    fullHeight?: boolean;
    subSidebarWidth?: string;
}

const CommandPaletteTrigger: FC = () => {
    const { t } = useTranslation('components/command-palette');

    const handleClick = () => {
        window.dispatchEvent(new CustomEvent('command-palette:open'));
    };

    return (
        <Button
            variant="outline"
            size="sm"
            onClick={handleClick}
            className="gap-2 h-9"
            aria-label={t('triggerLabel')}
            data-testid="command-palette-trigger"
        >
            <WhoDBChatIcon name="search" />
            <span className="ce-command-label hidden sm:inline text-xs text-neutral-500 dark:text-neutral-400">{t('triggerLabel')}</span>
            <Kbd className="ce-command-shortcut hidden sm:inline-flex h-5 px-1">
                {isMacPlatform ? '⌘K' : 'Ctrl+K'}
            </Kbd>
        </Button>
    );
};

const KeyboardShortcutsHint: FC = () => {
    const { t } = useTranslation('components/keyboard-shortcuts-help');

    const handleClick = () => {
        // Dispatch a keyboard event to trigger the shortcuts modal
        window.dispatchEvent(new KeyboardEvent('keydown', { key: '?' }));
    };

    return (
        <Tooltip>
            <TooltipTrigger>
                <Button
                    variant="outline"
                    size="sm"
                    onClick={handleClick}
                    className="ce-help-trigger gap-1.5 h-9"
                    aria-label={t('showShortcuts')}
                >
                    <WhoDBChatIcon name="help" />
                </Button>
            </TooltipTrigger>
            <TooltipContent side="bottom">
                <p>{t('hint')}</p>
            </TooltipContent>
        </Tooltip>
    );
};

const ThemeToggle: FC = () => {
    const {t} = useTranslation('components/page');
    const {theme, setTheme} = useTheme();
    const dark = theme === 'dark' || (theme === 'system' && window.matchMedia('(prefers-color-scheme: dark)').matches);

    return <Button variant="outline" size="icon" className="ce-theme-trigger" aria-label={t('theme')} onClick={() => { setTheme(dark ? 'light' : 'dark'); }}>
        <WhoDBChatIcon name={dark ? 'sun' : 'moon'} />
    </Button>;
};

const MIN_SIDEBAR_WIDTH = 180;
const MAX_MAIN_SIDEBAR_WIDTH = 520;
const MAX_SUB_SIDEBAR_WIDTH = 640;

/** Renders the authenticated app shell and optional full-height content panels. */
export const InternalPage: FC<IInternalPageProps> = (props) => {
    const { t } = useTranslation('components/page');
    const isLoggedIn = useAppSelector(state => state.auth.current != null);
    const sidebarOpen = useAppSelector(state => state.settings.sidebarOpen);
    const sidebarWidth = useAppSelector(state => state.settings.sidebarWidth ?? 256);
    const subSidebarWidth = useAppSelector(state => state.settings.subSidebarWidth ?? 256);
    const isTablesPage = props.routes?.at(-1)?.path === InternalRoutes.Dashboard.StorageUnit.path;
    const isExplorePage = props.routes?.at(-1)?.path === InternalRoutes.Dashboard.ExploreStorageUnit.path;
    const isWorkspacePage = [InternalRoutes.RawExecute.path, InternalRoutes.Settings?.path, InternalRoutes.ContactUs?.path].includes(props.routes?.at(-1)?.path);
    const dispatch = useAppDispatch();
    const [resizingColumn, setResizingColumn] = useState<'main' | 'sub' | null>(null);
    const resizeStateRef = useRef<{ column: 'main' | 'sub'; startX: number; startWidth: number; zoom: number } | null>(null);

    const startResize = useCallback((column: 'main' | 'sub', startWidth: number, e: ReactMouseEvent<HTMLDivElement>) => {
        e.preventDefault();
        const zoom = Number.parseFloat(window.getComputedStyle(document.body).zoom) || 1;
        resizeStateRef.current = {
            column,
            startX: e.clientX,
            startWidth,
            zoom,
        };
        setResizingColumn(column);
    }, []);

    useEffect(() => {
        if (!resizingColumn) return;

        const prevCursor = document.body.style.cursor;
        const prevUserSelect = document.body.style.userSelect;
        document.body.style.cursor = 'col-resize';
        document.body.style.userSelect = 'none';

        const handleMouseMove = (e: MouseEvent) => {
            const state = resizeStateRef.current;
            if (!state) return;
            const delta = (e.clientX - state.startX) / state.zoom;
            if (state.column === 'main') {
                const nextWidth = Math.round(Math.min(MAX_MAIN_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, state.startWidth + delta)));
                dispatch(SettingsActions.setSidebarWidth(nextWidth));
            } else {
                const nextWidth = Math.round(Math.min(MAX_SUB_SIDEBAR_WIDTH, Math.max(MIN_SIDEBAR_WIDTH, state.startWidth + delta)));
                dispatch(SettingsActions.setSubSidebarWidth(nextWidth));
            }
        };

        const handleMouseUp = () => {
            resizeStateRef.current = null;
            setResizingColumn(null);
        };

        window.addEventListener('mousemove', handleMouseMove);
        window.addEventListener('mouseup', handleMouseUp);
        return () => {
            document.body.style.cursor = prevCursor;
            document.body.style.userSelect = prevUserSelect;
            window.removeEventListener('mousemove', handleMouseMove);
            window.removeEventListener('mouseup', handleMouseUp);
        };
    }, [dispatch, resizingColumn]);

    // Fetch source session metadata when logged in so Apollo state is ready
    // for source-operator and column-type helpers.
    useSourceSessionMetadata();

    return (
        <Container>
            <div className="flex flex-row shrink-0 h-full" data-resizing={resizingColumn != null ? 'true' : undefined}>
                <div className="relative h-full shrink-0">
                    <SidebarProvider
                        open={sidebarOpen}
                        onOpenChange={(open) => dispatch(SettingsActions.setSidebarOpen(open))}
                        className="w-auto shrink-0"
                        style={{ '--sidebar-width': `${sidebarWidth}px` } as CSSProperties}
                    >
                        <Sidebar />
                    </SidebarProvider>
                    {sidebarOpen && (
                        <div
                            role="separator"
                            aria-orientation="vertical"
                            data-testid="main-sidebar-resizer"
                            onMouseDown={(e) => { startResize('main', sidebarWidth, e); }}
                            className={cn(
                                "hidden md:block absolute top-0 -right-1 bottom-0 w-2 cursor-col-resize z-[60] transition-colors",
                                resizingColumn === 'main' ? "bg-primary/60" : "hover:bg-primary/40"
                            )}
                        />
                    )}
                </div>
                {props.sidebar && (
                    <div className="relative h-full shrink-0">
                        <SidebarProvider
                            className="w-auto shrink-0"
                            style={{ '--sidebar-width': props.subSidebarWidth ?? `${subSidebarWidth}px` } as CSSProperties}
                        >
                            {props.sidebar}
                        </SidebarProvider>
                        <div
                            role="separator"
                            aria-orientation="vertical"
                            data-testid="sub-sidebar-resizer"
                            onMouseDown={(e) => { startResize('sub', subSidebarWidth, e); }}
                            className={cn(
                                "hidden md:block absolute top-0 -right-1 bottom-0 w-2 cursor-col-resize z-[60] transition-colors",
                                resizingColumn === 'sub' ? "bg-primary/60" : "hover:bg-primary/40"
                            )}
                        />
                    </div>
                )}
            </div>
            <Page {...props} wrapperClassName={cn('p-0', isTablesPage && 'ce-tables-shell', isExplorePage && 'ce-explore-shell', isWorkspacePage && 'ce-workspace-shell')}>
                <div className={cn("flex flex-col grow py-6", (isTablesPage || isExplorePage || isWorkspacePage) && 'ce-app-content', props.fullHeight && "min-h-0 !py-0")}>
                    <div className={cn("flex flex-col gap-1 px-8", (isTablesPage || isExplorePage || isWorkspacePage) && 'ce-tables-topbar', props.fullHeight && !isWorkspacePage && "px-4 py-5")}>
                        <div className="flex w-full justify-between items-center gap-2">
                            <Button
                                variant="outline"
                                size="icon"
                                className="md:hidden shrink-0"
                                aria-label={t('openNavigation')}
                                data-testid="mobile-nav-toggle"
                                onClick={() => window.dispatchEvent(new CustomEvent('menu:toggle-sidebar'))}
                            >
                                <Bars3Icon className="h-4 w-4" />
                            </Button>
                            <Breadcrumb routes={props.routes ?? []} active={props.routes?.at(-1)} />
                            <div className="flex items-center gap-2 shrink-0">
                                <CommandPaletteTrigger />
                                <KeyboardShortcutsHint />
                                <div data-testid="mode-toggle" role="group" aria-label={t('theme')}>
                                    <ThemeToggle />
                                </div>
                            </div>
                        </div>
                        {!isTablesPage && <ConnectionContext />}
                    </div>
                    {
                        !isLoggedIn
                        ? <div className="flex justify-center items-center h-full">
                            <Loading size="lg" />
                        </div>
                            : <main
                                id="main-content"
                                className={cn("flex grow flex-wrap gap-sm py-4 content-start relative px-8", props.fullHeight && "min-h-0 !flex-nowrap !px-4 !pt-3 !pb-0", isWorkspacePage && 'ce-workspace-content')}
                                data-testid="page-content"
                                tabIndex={-1}
                            >
                            {props.children}
                        </main>
                    }
                </div>
            </Page>
        </Container>
    )
}

type IContainerProps = {
    children?: ReactNode;
    className?: string;
}

export const Container: FC<IContainerProps> = ({ className, children }) => {
    return  <div className={classNames(className, "flex grow h-full w-full overflow-hidden")}>
        {children}
    </div>
}
