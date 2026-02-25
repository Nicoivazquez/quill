import { useEffect, useRef, useCallback } from 'react';
import { useAuthStore } from '../store/authStore';

const AUTH_PATH_PREFIX = '/api/v1/auth/';

let originalFetchImpl: typeof window.fetch | null = null;
let fetchInterceptorInstalled = false;
let getTokenHandler: (() => string | null) | null = null;
let refreshHandler: (() => Promise<string | null>) | null = null;
let logoutHandler: (() => void) | null = null;

function getRequestPath(input: RequestInfo | URL): string {
    if (typeof input === 'string') {
        return new URL(input, window.location.origin).pathname;
    }
    if (input instanceof URL) {
        return input.pathname;
    }
    return new URL(input.url, window.location.origin).pathname;
}

function withAuthorizationHeader(input: RequestInfo | URL, init: RequestInit | undefined, token: string): RequestInit {
    const headers = new Headers(init?.headers);

    if (input instanceof Request) {
        input.headers.forEach((value, key) => {
            if (!headers.has(key)) {
                headers.set(key, value);
            }
        });
    }

    headers.set('Authorization', `Bearer ${token}`);

    return {
        ...init,
        headers,
    };
}

export function useAuth() {
    const {
        token,
        requiresRegistration,
        isInitialized,
        setToken,
        setRequiresRegistration,
        setInitialized,
        logout: storeLogout
    } = useAuthStore();

    const isAuthenticated = !!token;

    const tokenCheckIntervalRef = useRef<NodeJS.Timeout | null>(null);
    const refreshInFlightRef = useRef<Promise<string | null> | null>(null);

    const getAuthHeaders = useCallback((): Record<string, string> => {
        if (token) {
            return { Authorization: `Bearer ${token}` };
        }
        return {};
    }, [token]);

    // Memoize expensive token expiry check
    const isTokenExpired = useCallback((tokenToCheck: string): boolean => {
        try {
            const payload = JSON.parse(atob(tokenToCheck.split(".")[1]));
            const currentTime = Date.now() / 1000;
            // Check if token will expire in the next 5 minutes
            return payload.exp && payload.exp <= (currentTime + 300);
        } catch (error) {
            console.error("Invalid token format:", error);
            return true;
        }
    }, []);

    const logout = useCallback(() => {
        storeLogout();
        fetch("/api/v1/auth/logout", {
            method: "POST",
            headers: {
                "Authorization": token ? `Bearer ${token}` : "",
            },
        }).catch(() => { });

        if (window.location.pathname !== "/") {
            // Force navigation handled by RouterContext or window.location if critical
            window.history.pushState({ route: { path: 'home' } }, "", "/");
            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            window.dispatchEvent(new PopStateEvent('popstate', { state: { route: { path: 'home' } } as any }));
        }
    }, [token, storeLogout]);


    const login = useCallback((newToken: string) => {
        setToken(newToken);
        setRequiresRegistration(false);
    }, [setToken, setRequiresRegistration]);


    const tryRefresh = useCallback(async (): Promise<string | null> => {
        if (refreshInFlightRef.current) {
            return refreshInFlightRef.current;
        }

        const refreshPromise = (async (): Promise<string | null> => {
            try {
                const res = await fetch('/api/v1/auth/refresh', { method: 'POST' });
                if (!res.ok) return null;

                const data = await res.json();
                if (typeof data?.token === 'string' && data.token.length > 0) {
                    login(data.token);
                    return data.token;
                }
                return null;
            } catch {
                return null;
            }
        })();

        refreshInFlightRef.current = refreshPromise;

        try {
            return await refreshPromise;
        } finally {
            refreshInFlightRef.current = null;
        }
    }, [login]);


    // Consolidated token management
    useEffect(() => {
        getTokenHandler = () => useAuthStore.getState().token;
        refreshHandler = tryRefresh;
        logoutHandler = logout;

        if (!fetchInterceptorInstalled) {
            originalFetchImpl = window.fetch.bind(window);

            const wrappedFetch: typeof window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
                const requestPath = getRequestPath(input);
                const fetchImpl = originalFetchImpl ?? window.fetch.bind(window);

                let res = await fetchImpl(input, init);
                if (res.status !== 401) {
                    return res;
                }

                // Prevent refresh recursion and avoid auth endpoint interception.
                if (requestPath.startsWith(AUTH_PATH_PREFIX)) {
                    return res;
                }

                const currentToken = getTokenHandler ? getTokenHandler() : null;
                if (!currentToken) {
                    return res;
                }

                const newToken = refreshHandler ? await refreshHandler() : null;
                if (!newToken) {
                    logoutHandler?.();
                    return res;
                }

                res = await fetchImpl(input, withAuthorizationHeader(input, init, newToken));
                if (res.status === 401) {
                    logoutHandler?.();
                    return res;
                }

                return res;
            };

            // eslint-disable-next-line @typescript-eslint/no-explicit-any
            window.fetch = wrappedFetch as any;
            fetchInterceptorInstalled = true;
        }

        if (tokenCheckIntervalRef.current) clearInterval(tokenCheckIntervalRef.current);

        if (token) {
            const checkTokenExpiry = async () => {
                if (!token) return;
                if (isTokenExpired(token)) {
                    const newToken = await tryRefresh();
                    if (!newToken) logout();
                }
            };
            tokenCheckIntervalRef.current = setInterval(checkTokenExpiry, 60000);
            checkTokenExpiry();
        }

        return () => {
            if (tokenCheckIntervalRef.current) clearInterval(tokenCheckIntervalRef.current);
        };
    }, [token, isTokenExpired, logout, tryRefresh]);

    // Initial check (equivalent to old AuthProvider mount effect)
    useEffect(() => {
        const initializeAuth = async () => {
            if (isInitialized) return; // Don't run if already initialized

            try {
                const response = await fetch("/api/v1/auth/registration-status");
                if (response.ok) {
                    const data = await response.json();
                    const regEnabled = typeof data.registration_enabled === 'boolean' ? data.registration_enabled : !!data.requiresRegistration;
                    setRequiresRegistration(regEnabled);

                    if (!regEnabled) {
                        // Check token validity if present
                        if (token && isTokenExpired(token)) {
                            // Try refresh or logout
                            const Refreshed = await tryRefresh();
                            if (!Refreshed) logout();
                        }
                    }
                }
            } catch (error) {
                console.error("Failed check reg status", error);
            } finally {
                setInitialized(true);
            }
        };
        initializeAuth();
    }, [isInitialized, setRequiresRegistration, setInitialized, token, isTokenExpired, tryRefresh, logout]);

    return {
        token,
        isAuthenticated,
        requiresRegistration,
        isInitialized,
        login,
        logout,
        getAuthHeaders
    };
}
