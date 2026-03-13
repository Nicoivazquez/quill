
import React from 'react';

interface MainLayoutProps {
    children: React.ReactNode;
    header: React.ReactNode;
    sidebar?: React.ReactNode;
    className?: string;
}

export const MainLayout = ({ children, header, sidebar, className = "" }: MainLayoutProps) => {
    return (
        <div className={`min-h-screen bg-[var(--bg-main)] ${className}`}>
            <div className="mx-auto w-full max-w-[1360px] px-3 sm:px-5 lg:px-8 py-4 sm:py-6">
                <div className="mb-5 sm:mb-6">
                    {header}
                </div>
                {sidebar ? (
                    <div className="flex gap-5">
                        <aside className="hidden lg:block w-56 flex-shrink-0">
                            <div className="obsidian-pane sticky top-4 max-h-[calc(100vh-6rem)] overflow-hidden flex flex-col rounded-[var(--radius-card)]">
                                {sidebar}
                            </div>
                        </aside>
                        <main className="flex-1 min-w-0">
                            {children}
                        </main>
                    </div>
                ) : (
                    <main className="w-full">
                        {children}
                    </main>
                )}
            </div>
        </div>
    );
};
