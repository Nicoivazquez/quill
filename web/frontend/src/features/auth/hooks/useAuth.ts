import { useEffect, useRef, useCallback } from 'react';
import { useAuthStore } from '../store/authStore';

declare global {
    interface Window {
        __scriberr_original_fetch?: typeof window.fetch;
    }
}

interface SetupStateResponse {
    completed?: boolean;
    auth_mode?: string;
}

export function useAuth() {
    const {
        token,
        isAuthenticated,
        isLocalMode,
        isSetupCompleted,
        requiresRegistration,
        isInitialized,
        setToken,
        setLocalMode,
        setSetupCompleted,
        setRequiresRegistration,
        setInitialized,
        logout: storeLogout,
    } = useAuthStore();

    const tokenCheckIntervalRef = useRef<NodeJS.Timeout | null>(null);
    const fetchWrapperSetupRef = useRef(false);

    const getAuthHeaders = useCallback((): Record<string, string> => {
        if (!isLocalMode && token) {
            return { Authorization: `Bearer ${token}` };
        }
        return {};
    }, [isLocalMode, token]);

    const isTokenExpired = useCallback((tokenToCheck: string): boolean => {
        try {
            const payload = JSON.parse(atob(tokenToCheck.split('.')[1]));
            const currentTime = Date.now() / 1000;
            return payload.exp && payload.exp <= (currentTime + 300);
        } catch (error) {
            console.error('Invalid token format:', error);
            return true;
        }
    }, []);

    const logout = useCallback(() => {
        if (!isLocalMode) {
            fetch('/api/v1/auth/logout', {
                method: 'POST',
                headers: {
                    Authorization: token ? `Bearer ${token}` : '',
                },
            }).catch(() => { });
        }

        storeLogout();

        if (window.location.pathname !== '/') {
            window.history.pushState({ route: { path: 'home' } }, '', '/');
            window.dispatchEvent(new PopStateEvent('popstate', { state: { route: { path: 'home' } } }));
        }
    }, [isLocalMode, token, storeLogout]);

    const login = useCallback((newToken: string) => {
        setToken(newToken);
        setLocalMode(false);
        setRequiresRegistration(false);
    }, [setToken, setLocalMode, setRequiresRegistration]);

    const tryRefresh = useCallback(async (): Promise<string | null> => {
        if (isLocalMode) {
            return null;
        }

        try {
            const fetchToUse = window.__scriberr_original_fetch || window.fetch;
            const res = await fetchToUse('/api/v1/auth/refresh', { method: 'POST' });
            if (!res.ok) return null;
            const data = await res.json();
            if (data?.token) {
                login(data.token);
                return data.token as string;
            }
            return null;
        } catch {
            return null;
        }
    }, [isLocalMode, login]);

    const refreshSetupState = useCallback(async (): Promise<SetupStateResponse | null> => {
        try {
            const fetchToUse = window.__scriberr_original_fetch || window.fetch;
            const response = await fetchToUse('/api/v1/setup/state');
            if (!response.ok) return null;

            const state = (await response.json()) as SetupStateResponse;
            const mode = typeof state.auth_mode === 'string' ? state.auth_mode.toLowerCase() : 'local';
            const localMode = mode !== 'server';

            setLocalMode(localMode);
            setSetupCompleted(!!state.completed);

            if (localMode) {
                setRequiresRegistration(false);
            }

            return state;
        } catch {
            return null;
        }
    }, [setLocalMode, setSetupCompleted, setRequiresRegistration]);

    useEffect(() => {
        if (!fetchWrapperSetupRef.current) {
            if (!window.__scriberr_original_fetch) {
                window.__scriberr_original_fetch = window.fetch.bind(window);
            }

            const originalFetch = window.__scriberr_original_fetch;
            const wrappedFetch: typeof window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
                if (isLocalMode) {
                    return originalFetch(input, init);
                }

                const url = typeof input === 'string' ? input : (input instanceof URL ? input.href : input.url);
                const isAuthEndpoint = url.includes('/api/v1/auth/');

                let res = await originalFetch(input, init);
                if (res.status === 401 && !isAuthEndpoint) {
                    const newToken = await tryRefresh();
                    if (newToken) {
                        const newInit: RequestInit = init ? { ...init } : {};
                        const headers = new Headers(newInit.headers);
                        headers.set('Authorization', `Bearer ${newToken}`);
                        newInit.headers = headers;

                        res = await originalFetch(input, newInit);
                        if (res.status !== 401) return res;
                    }
                    logout();
                }
                return res;
            };
            window.fetch = wrappedFetch;
            fetchWrapperSetupRef.current = true;
        }

        if (tokenCheckIntervalRef.current) clearInterval(tokenCheckIntervalRef.current);

        if (!isLocalMode && token) {
            const checkTokenExpiry = async () => {
                if (!token) return;
                if (isTokenExpired(token)) {
                    const refreshedToken = await tryRefresh();
                    if (!refreshedToken) logout();
                }
            };

            tokenCheckIntervalRef.current = setInterval(checkTokenExpiry, 60000);
            checkTokenExpiry();
        }

        return () => {
            if (tokenCheckIntervalRef.current) clearInterval(tokenCheckIntervalRef.current);
        };
    }, [isLocalMode, token, isTokenExpired, tryRefresh, logout]);

    useEffect(() => {
        const initializeAuth = async () => {
            if (isInitialized) return;

            try {
                const setupState = await refreshSetupState();
                const mode = setupState?.auth_mode?.toLowerCase() ?? 'local';
                const setupCompleted = !!setupState?.completed;

                if (!setupCompleted) {
                    return;
                }

                if (mode !== 'server') {
                    setRequiresRegistration(false);
                    return;
                }

                const response = await fetch('/api/v1/auth/registration-status');
                if (response.ok) {
                    const data = await response.json();
                    const regEnabled = typeof data.registration_enabled === 'boolean'
                        ? data.registration_enabled
                        : !!data.requiresRegistration;

                    setRequiresRegistration(regEnabled);

                    if (!regEnabled && token && isTokenExpired(token)) {
                        const refreshedToken = await tryRefresh();
                        if (!refreshedToken) logout();
                    }
                }
            } catch (error) {
                console.error('Failed check reg status', error);
            } finally {
                setInitialized(true);
            }
        };

        initializeAuth();
    }, [
        isInitialized,
        refreshSetupState,
        setRequiresRegistration,
        setInitialized,
        token,
        isTokenExpired,
        tryRefresh,
        logout,
    ]);

    return {
        token,
        isAuthenticated,
        isLocalMode,
        isSetupCompleted,
        requiresRegistration,
        isInitialized,
        login,
        logout,
        getAuthHeaders,
        refreshSetupState,
    };
}
