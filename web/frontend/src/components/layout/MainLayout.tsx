
import React from 'react';

interface MainLayoutProps {
    children: React.ReactNode;
    header: React.ReactNode;
    className?: string;
}

export const MainLayout = ({ children, header, className = "" }: MainLayoutProps) => {
    return (
        <div className={`min-h-screen bg-[var(--bg-main)] ${className}`}>
            <div className="mx-auto w-full max-w-[1360px] px-3 sm:px-5 lg:px-8 py-4 sm:py-6">
                <div className="mb-5 sm:mb-6">
                    {header}
                </div>
                <main className="w-full">
                    {children}
                </main>
            </div>
        </div>
    );
};
