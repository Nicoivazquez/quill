import { create } from 'zustand';
import { persist } from 'zustand/middleware';

interface AuthState {
    token: string | null;
    isAuthenticated: boolean;
    isLocalMode: boolean;
    isSetupCompleted: boolean;
    requiresRegistration: boolean;
    isInitialized: boolean;
    setToken: (token: string | null) => void;
    setLocalMode: (isLocalMode: boolean) => void;
    setSetupCompleted: (isSetupCompleted: boolean) => void;
    setRequiresRegistration: (requires: boolean) => void;
    setInitialized: (initialized: boolean) => void;
    logout: () => void;
}

export const useAuthStore = create<AuthState>()(
    persist(
        (set) => ({
            token: null,
            isAuthenticated: false,
            isLocalMode: false,
            isSetupCompleted: false,
            requiresRegistration: false,
            isInitialized: false,
            setToken: (token) => set((state) => ({ token, isAuthenticated: state.isLocalMode || !!token })),
            setLocalMode: (isLocalMode) => set((state) => ({ isLocalMode, isAuthenticated: isLocalMode || !!state.token })),
            setSetupCompleted: (isSetupCompleted) => set({ isSetupCompleted }),
            setRequiresRegistration: (requires) => set({ requiresRegistration: requires }),
            setInitialized: (initialized) => set({ isInitialized: initialized }),
            logout: () => {
                set((state) => ({ token: null, isAuthenticated: state.isLocalMode }));
                localStorage.removeItem('auth-storage');
                // Optional: Call logout endpoint if needed, but side effects strictly in hooks/components usually better
            },
        }),
        {
            name: 'auth-storage',
            partialize: (state) => ({ token: state.token, isLocalMode: state.isLocalMode }),
        }
    )
);
