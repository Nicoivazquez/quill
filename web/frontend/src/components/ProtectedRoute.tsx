import type { ReactNode } from "react";
import { useAuth } from "@/features/auth/hooks/useAuth";
import { Login } from "@/features/auth/components/Login";
import { Register } from "@/features/auth/components/Register";
import { SetupWizard } from "@/features/setup/components/SetupWizard";

interface ProtectedRouteProps {
	children: ReactNode;
}

export function ProtectedRoute({ children }: ProtectedRouteProps) {
	const { isAuthenticated, isLocalMode, isSetupCompleted, requiresRegistration, isInitialized, login, refreshSetupState } = useAuth();

	// Show loading while initializing
	if (!isInitialized) {
		return (
			<div className="min-h-screen bg-[var(--bg-main)] flex items-center justify-center">
				<div className="text-center">
					<div className="animate-spin rounded-full h-8 w-8 border-b-2 border-[var(--brand-solid)] mx-auto"></div>
					<p className="mt-2 text-[var(--text-secondary)]">Loading...</p>
				</div>
			</div>
		);
	}

	if (!isSetupCompleted) {
		return (
			<SetupWizard
				onComplete={async () => {
					await refreshSetupState();
					window.location.reload();
				}}
			/>
		);
	}

	// Show registration form if no users exist
	if (!isLocalMode && requiresRegistration) {
		return <Register onRegister={login} />;
	}

	// Show login form if not authenticated
	if (!isLocalMode && !isAuthenticated) {
		return <Login onLogin={login} />;
	}

	// Show protected content if authenticated
	return <>{children}</>;
}
