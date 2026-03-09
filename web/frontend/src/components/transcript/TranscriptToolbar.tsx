import React from 'react';
import {
    List,
    AlignLeft,
    ArrowDownCircle,
    StickyNote,
    Info,
    FileText,
    Users,
    Sparkles,
    Download,
    FileImage,
    FileJson,
    MessageCircle
} from 'lucide-react';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger
} from '@/components/ui/dropdown-menu';
import { cn } from '@/lib/utils';
import type { Note } from '@/types/note';

interface TranscriptToolbarProps {
    transcriptMode: 'compact' | 'expanded';
    setTranscriptMode: (mode: 'compact' | 'expanded') => void;
    autoScrollEnabled: boolean;
    setAutoScrollEnabled: (enabled: boolean) => void;
    notesOpen: boolean;
    setNotesOpen: (open: boolean) => void;
    notes: Note[];
    onOpenExecutionInfo: () => void;
    onOpenLogs: () => void;
    hasSpeakers: boolean;
    detectedSpeakersCount: number;
    onOpenSpeakerRename: () => void;
    onOpenSummarize: () => void;
    llmReady: boolean | null;
    onDownloadSRT: () => void;
    onDownloadTXT: () => void;
    onDownloadJSON: () => void;
    onOpenChat: () => void;
    className?: string;
}

export const TranscriptToolbar: React.FC<TranscriptToolbarProps> = ({
    transcriptMode,
    setTranscriptMode,
    autoScrollEnabled,
    setAutoScrollEnabled,
    notesOpen,
    setNotesOpen,
    notes,
    onOpenExecutionInfo,
    onOpenLogs,
    hasSpeakers,
    detectedSpeakersCount,
    onOpenSpeakerRename,
    onOpenSummarize,
    llmReady,
    onDownloadSRT,
    onDownloadTXT,
    onDownloadJSON,
    onOpenChat,
    className
}) => {
    return (
        <div className={cn(
            "flex items-center gap-1 p-1.5 rounded-[var(--radius-card)] border border-[var(--border-subtle)] shadow-[var(--shadow-card)] transition-all duration-300",
            "bg-[var(--bg-card)]",
            className
        )}>
            {/* View Mode Toggle */}
            <ToolbarButton
                active={transcriptMode === 'compact'}
                onClick={() => setTranscriptMode(transcriptMode === 'compact' ? 'expanded' : 'compact')}
                title={transcriptMode === 'compact' ? 'Switch to Timeline View' : 'Switch to Compact View'}
            >
                {transcriptMode === 'compact' ? <List className="w-4 h-4" /> : <AlignLeft className="w-4 h-4" />}
            </ToolbarButton>

            {/* Auto Scroll Toggle */}
            <ToolbarButton
                active={autoScrollEnabled}
                onClick={() => setAutoScrollEnabled(!autoScrollEnabled)}
                title={autoScrollEnabled ? 'Disable Auto-Scroll' : 'Enable Auto-Scroll'}
            >
                <ArrowDownCircle className="w-4 h-4" />
            </ToolbarButton>

            <Divider />

            {/* Notes Toggle */}
            <ToolbarButton
                active={notesOpen}
                onClick={() => setNotesOpen(!notesOpen)}
                title="Toggle Notes"
                className="relative"
            >
                <StickyNote className="w-4 h-4" />
                {notes.length > 0 && (
                    <span className="absolute -top-1 -right-1 min-w-[16px] h-[16px] flex items-center justify-center rounded-full bg-[var(--brand-solid)] text-[10px] font-bold text-white ring-2 ring-[var(--bg-card)]">
                        {notes.length > 99 ? '99+' : notes.length}
                    </span>
                )}
            </ToolbarButton>

            {/* Speaker Rename (Conditional) */}
            {hasSpeakers && detectedSpeakersCount > 0 && (
                <ToolbarButton
                    onClick={onOpenSpeakerRename}
                    title="Rename Speakers"
                >
                    <Users className="w-4 h-4" />
                </ToolbarButton>
            )}

            {/* Summarize */}
            <ToolbarButton
                onClick={onOpenSummarize}
                disabled={llmReady === false}
                title={llmReady === false ? 'Configure LLM in Settings' : 'Summarize Transcript'}
                className={cn(llmReady === false && "opacity-50 cursor-not-allowed")}
            >
                <Sparkles className="w-4 h-4" />
            </ToolbarButton>

            <Divider />

            {/* Info & Logs Group */}
            <div className="flex items-center gap-0.5">
                <ToolbarButton onClick={onOpenExecutionInfo} title="Execution Info">
                    <Info className="w-4 h-4" />
                </ToolbarButton>
                <ToolbarButton onClick={onOpenLogs} title="View Logs">
                    <FileText className="w-4 h-4" />
                </ToolbarButton>
            </div>

            <Divider />

            {/* Download Menu */}
            <DropdownMenu>
                <DropdownMenuTrigger asChild>
                    <button
                        className={cn(
                            "flex items-center justify-center w-8 h-8 rounded-[var(--radius-btn)] transition-all duration-200",
                            "text-[var(--text-secondary)] hover:text-[var(--text-primary)]",
                            "hover:bg-[var(--bg-muted-pane)]",
                            "focus:outline-none focus:ring-2 focus:ring-[var(--brand-solid)]/20"
                        )}
                        title="Download"
                    >
                        <Download className="w-4 h-4" />
                    </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="w-48 bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-[var(--shadow-float)]">
                    <DropdownMenuItem onClick={onDownloadSRT} className="cursor-pointer">
                        <FileImage className="w-4 h-4 mr-2" />
                        <span>Download SRT</span>
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={onDownloadTXT} className="cursor-pointer">
                        <AlignLeft className="w-4 h-4 mr-2" />
                        <span>Download Text</span>
                    </DropdownMenuItem>
                    <DropdownMenuItem onClick={onDownloadJSON} className="cursor-pointer">
                        <FileJson className="w-4 h-4 mr-2" />
                        <span>Download JSON</span>
                    </DropdownMenuItem>
                </DropdownMenuContent>
            </DropdownMenu>

            <Divider />

            {/* Chat Button */}
            <ToolbarButton onClick={onOpenChat} title="Open Chat">
                <MessageCircle className="w-4 h-4" />
            </ToolbarButton>
        </div>
    );
};

// Sub-components for cleaner code
const ToolbarButton: React.FC<{
    active?: boolean;
    disabled?: boolean;
    onClick?: () => void;
    title?: string;
    children: React.ReactNode;
    className?: string;
}> = ({ active, disabled, onClick, title, children, className }) => (
    <button
        type="button"
        onClick={onClick}
        disabled={disabled}
        title={title}
        className={cn(
            "flex items-center justify-center w-8 h-8 rounded-[var(--radius-btn)] transition-all duration-200",
            "text-[var(--text-secondary)]",
            active
                ? "bg-[var(--brand-light)] text-[var(--brand-solid)] shadow-sm ring-1 ring-[var(--brand-solid)]/20"
                : "hover:bg-[var(--bg-muted-pane)] hover:text-[var(--text-primary)]",
            disabled && "opacity-50 cursor-not-allowed hover:bg-transparent",
            className
        )}
    >
        {children}
    </button>
);

const Divider = () => (
    <div className="w-px h-4 bg-[var(--border-subtle)] mx-1" />
);
