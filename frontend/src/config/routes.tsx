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

import type { FC, ReactNode} from "react";
import { lazy, Suspense } from "react";
import { Navigate, Outlet } from "react-router-dom";
import { useAppSelector } from "../store/hooks";
import { LogoutPage } from "../pages/auth/logout";
import { getComponent } from "./component-registry";
import { featureFlags } from "./features";
import { LoadingPage } from "../components/loading";
import { AuthSessionGuard as CEAuthSessionGuard } from "../components/auth-session-guard";
import { getRegisteredUnscopedRoutes, getSurfaceFallbackPath, isUnscopedRouteEnabled } from "./route-registry";
import { useSourceContract } from "../hooks/useSourceContract";
export { registerRoute } from "./route-registry";

import { GraphPage } from "../pages/graph/graph";
import { ExploreStorageUnit } from "../pages/storage-unit/explore-storage-unit";
import { StorageUnitPage } from "../pages/storage-unit/storage-unit";
import { RawExecutePage } from "../pages/raw-execute/raw-execute";
import { DataCopyPage } from "../pages/data-copy/data-copy";
import { MigrationToolPage } from "../pages/migration-tool/migration-tool";
import { SettingsPage as CESettingsPage } from "../pages/settings/settings";
import { ContactUsPage } from "../pages/contact-us/contact-us";

// Allow EE to override the login page via the component registry (e.g. for SSO).
// Falls back to the CE database-credential login page when no override is registered.
// Must be lazy — getComponent must run at render time (after EE register.ts populates the registry).
const CELoginPage = lazy(() => import("../pages/auth/login").then(m => ({ default: m.LoginPage })));
const LoginPage = () => {
    const Registered = getComponent('login-page');
    if (Registered) return <Registered />;
    return <CELoginPage />;
};

const SettingsPage = __WHODB_EDITION__ === "ce" ? CESettingsPage : null;

/** Redirects `/` to the registered default route or the storage unit browser. */
export const NavigateToDefault: FC = () => {
    const DefaultNavigateOverride = getComponent('default-navigate');
    if (DefaultNavigateOverride) {
        return <Suspense fallback={null}><DefaultNavigateOverride /></Suspense>;
    }
    return <Navigate to={getSurfaceFallbackPath()} replace />;
};

// Wrapper component for lazy loaded routes
const LazyRoute: FC<{ component: React.ComponentType<any> }> = ({ component: Component }) => (
  <Suspense fallback={<LoadingPage />}>
    <Component />
  </Suspense>
);

const SourceSurfaceRoute: FC<{
    surface: "graph" | "scratchpad";
    component: ReactNode;
}> = ({ surface, component }) => {
    const currentType = useAppSelector(state => state.auth.current?.Type);
    const { item, loading, supportsGraph, supportsScratchpad } = useSourceContract(currentType);

    if (!currentType || (loading && !item)) {
        return <LoadingPage />;
    }

    const isAllowed = surface === "graph" ? supportsGraph : supportsScratchpad;

    if (!isAllowed) {
        return <Navigate to={getSurfaceFallbackPath()} replace />;
    }

    return component;
};

export type IInternalRoute = {
    name: string;
    path: string;
    component: ReactNode;
    public?: boolean;
}

export const PublicRoutes = {
    Login: {
        name: "登录",
        path: "/login",
        component: <Suspense fallback={<LoadingPage />}><LoginPage /></Suspense>,
    },
}

export const InternalRoutes = {
    Dashboard: {
        StorageUnit: {
            name: "数据表", // should update on the page
            path: "/storage-unit",
            component: <LazyRoute component={StorageUnitPage} />,
        },
        ExploreStorageUnit: {
            name: "数据浏览",
            path: "/storage-unit/explore",
            component: <LazyRoute component={ExploreStorageUnit} />,
        },
    },
    Graph: {
        name: "关系图谱",
        path: "/graph",
        component: <SourceSurfaceRoute surface="graph" component={<LazyRoute component={GraphPage} />} />,
    },
    RawExecute: {
        name: "SQL 控制台",
        path: "/scratchpad",
        component: <SourceSurfaceRoute surface="scratchpad" component={<LazyRoute component={RawExecutePage} />} />,
    },
    DataCopy: {
        name: "数据复制",
        path: "/data-copy",
        component: <LazyRoute component={DataCopyPage} />,
    },
    MigrationTool: {
        name: "迁移工具",
        path: "/migration-tool",
        component: <LazyRoute component={MigrationToolPage} />,
    },
    Logout: {
        name: "退出登录",
        path: "/logout",
        component: <LogoutPage />,
    },
    ...(featureFlags.settingsPage && SettingsPage ? {
        Settings: {
            name: "系统设置",
            path: "/settings",
            component: <LazyRoute component={SettingsPage} />
        }
    } : {}),
    ...(featureFlags.contactUsPage ? {
        ContactUs: {
            name: "联系支持",
            path: "/contact-us",
            component: <LazyRoute component={ContactUsPage} />
        }
    } : {})
}

export const PrivateRoute: FC = () => {
    const loggedIn = useAppSelector(state => state.auth.status === "logged-in");
    const SetupGuard = getComponent('setup-guard') as FC<{ children: ReactNode }> | undefined;
    const AuthSessionGuard = getComponent('auth-session-guard') as FC<{ loggedIn: boolean; children: ReactNode }> | undefined;
    const children = SetupGuard ? (
        <Suspense fallback={<LoadingPage />}>
            <SetupGuard><Outlet /></SetupGuard>
        </Suspense>
    ) : <Outlet />;

    if (AuthSessionGuard) {
        return (
            <Suspense fallback={<LoadingPage />}>
                <AuthSessionGuard loggedIn={loggedIn}>{children}</AuthSessionGuard>
            </Suspense>
        );
    }

    return <CEAuthSessionGuard>{children}</CEAuthSessionGuard>;
}

export const getRoutes = (): IInternalRoute[] => {
    const allRoutes: IInternalRoute[] = [];
    const currentRoutes = Object.values(InternalRoutes);
    while (currentRoutes.length > 0) {
        const currentRoute = currentRoutes.shift();
        if (currentRoute == null) {
            continue;
        }
        if ("path" in currentRoute) {
            if (isUnscopedRouteEnabled(currentRoute.path)) {
                allRoutes.push(currentRoute);
            }
            continue;
        }
        currentRoutes.push(...Object.values((currentRoute)));
    }
    const extra = getRegisteredUnscopedRoutes().map(({ name, path, lazyComponent }) => ({
        name,
        path,
        component: <LazyRoute component={lazyComponent} />,
    }));
    return [...allRoutes, ...extra];
}
