import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
} from "@/components/ui/dialog";
import {
    Command,
    CommandEmpty,
    CommandGroup,
    CommandInput,
    CommandItem,
    CommandList
} from "@/components/ui/command";
import {
    Popover,
    PopoverContent,
    PopoverTrigger
} from "@/components/ui/popover";
import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/ui/toast";
import { useState, useEffect } from "react";
import ReactMarkdown from 'react-markdown';
import remarkMath from 'remark-math';
import rehypeRaw from 'rehype-raw';
import rehypeKatex from 'rehype-katex';
import rehypeHighlight from 'rehype-highlight';
import { useSummaryTemplates, useSummarizer, useAllSummaries, type SavedSummary } from "@/features/transcription/hooks/useTranscriptionSummary";

import { useTranscript, useAudioDetail, type Transcript } from "@/features/transcription/hooks/useAudioDetail";
import { useSpeakerMappings } from "@/features/transcription/hooks/useTranscriptionSpeakers";
import { formatSpeakerLabel } from "@/lib/speaker-utils";
import { useAuth } from "@/features/auth/hooks/useAuth";
import { useQueryClient } from "@tanstack/react-query";

import { Sparkles, Download, Copy, RefreshCw, ChevronDown, FileText, Plus, Trash2 } from "lucide-react";

// Helper function to format transcript with speaker labels
function formatTranscriptWithSpeakers(
    transcript: Transcript,
    speakerMappings: Record<string, string>
): string {
    if (!transcript.segments || transcript.segments.length === 0) {
        return transcript.text || '';
    }
    return transcript.segments.map(segment => {
        const speaker = speakerMappings[segment.speaker || ''] || formatSpeakerLabel(segment.speaker || 'UNKNOWN');
        return `[${speaker}] ${segment.text.trim()}`;
    }).join('\n');
}

function formatRelativeDate(dateStr: string): string {
    const date = new Date(dateStr);
    const now = new Date();
    const diffMs = now.getTime() - date.getTime();
    const diffMins = Math.floor(diffMs / 60000);
    if (diffMins < 1) return 'Just now';
    if (diffMins < 60) return `${diffMins}m ago`;
    const diffHours = Math.floor(diffMins / 60);
    if (diffHours < 24) return `${diffHours}h ago`;
    const diffDays = Math.floor(diffHours / 24);
    if (diffDays < 7) return `${diffDays}d ago`;
    return date.toLocaleDateString('en-US', { month: 'short', day: 'numeric' });
}

// Markdown renderer component
function MarkdownContent({ content }: { content: string }) {
    return (
        <div className="prose prose-stone dark:prose-invert max-w-none text-[var(--text-primary)] leading-relaxed">
            <ReactMarkdown
                remarkPlugins={[remarkMath]}
                rehypePlugins={[rehypeRaw as any, rehypeKatex as any, rehypeHighlight as any]} // eslint-disable-line @typescript-eslint/no-explicit-any
                components={{
                    p: ({ ...props }) => <p className="text-[var(--text-secondary)] leading-7 mb-4" {...props} />,
                    h1: ({ ...props }) => <h1 className="text-[var(--text-primary)] font-bold text-2xl mt-6 mb-4" {...props} />,
                    h2: ({ ...props }) => <h2 className="text-[var(--text-primary)] font-bold text-xl mt-6 mb-3" {...props} />,
                    h3: ({ ...props }) => <h3 className="text-[var(--text-primary)] font-bold text-lg mt-5 mb-2" {...props} />,
                    li: ({ ...props }) => <li className="pl-1 text-[var(--text-secondary)] mb-1" {...props} />,
                    strong: ({ ...props }) => <strong className="text-[var(--text-primary)] font-bold" {...props} />,
                    ul: ({ ...props }) => <ul className="list-disc pl-5 mb-4" {...props} />,
                    ol: ({ ...props }) => <ol className="list-decimal pl-5 mb-4" {...props} />,
                }}
            >
                {content}
            </ReactMarkdown>
        </div>
    );
}

interface SummaryDialogProps {
    audioId: string;
    isOpen: boolean;
    onClose: (open: boolean) => void;
    llmReady: boolean | null;
}

type ViewMode = 'list' | 'template-picker' | 'output';

export function SummaryDialog({ audioId, isOpen, onClose, llmReady }: SummaryDialogProps) {
    const { toast } = useToast();
    const { getAuthHeaders } = useAuth();
    const queryClient = useQueryClient();
    const { data: templates = [], isLoading: templatesLoading } = useSummaryTemplates();
    const { data: allSummaries = [], isLoading: summariesLoading } = useAllSummaries(audioId);
    const { data: transcript } = useTranscript(audioId, true);
    const { data: audioFile } = useAudioDetail(audioId);
    const { data: speakerMappings = {} } = useSpeakerMappings(audioId, true);

    const { generateSummary, isStreaming, streamContent, error } = useSummarizer(audioId);

    // State
    const [viewMode, setViewMode] = useState<ViewMode>('list');
    const [selectedSummary, setSelectedSummary] = useState<SavedSummary | null>(null);
    const [selectedTemplateId, setSelectedTemplateId] = useState<string>("");
    const [tplPopoverOpen, setTplPopoverOpen] = useState(false);

    const selectedTemplate = templates.find(t => t.id === selectedTemplateId);

    // When dialog opens, determine initial view
    useEffect(() => {
        if (isOpen) {
            setSelectedSummary(null);
            setSelectedTemplateId("");
            // If summaries exist, show list. Otherwise, show template picker.
            if (!summariesLoading && allSummaries.length > 0) {
                setViewMode('list');
                // Auto-select the latest summary
                setSelectedSummary(allSummaries[0]);
            } else if (!summariesLoading) {
                setViewMode('template-picker');
            }
        }
    }, [isOpen, summariesLoading, allSummaries]);

    // Reset when dialog closes
    useEffect(() => {
        if (!isOpen) {
            setViewMode('list');
            setSelectedSummary(null);
            setSelectedTemplateId("");
        }
    }, [isOpen]);

    const handleStartSummary = () => {
        if (!selectedTemplate || !transcript) return;
        setViewMode('output');
        setSelectedSummary(null);
        const includeSpeakers = selectedTemplate.include_speaker_info ?? false;
        const transcriptText = includeSpeakers
            ? formatTranscriptWithSpeakers(transcript, speakerMappings)
            : (transcript.text || '');
        generateSummary(selectedTemplate.id, selectedTemplate.model, selectedTemplate.prompt, transcriptText, includeSpeakers);
    };

    const handleRegenerate = (summary: SavedSummary) => {
        if (!transcript) return;
        // Find the matching template to get include_speaker_info
        const tpl = templates.find(t => t.id === summary.template_id);
        const includeSpeakers = tpl?.include_speaker_info ?? false;
        const transcriptText = includeSpeakers
            ? formatTranscriptWithSpeakers(transcript, speakerMappings)
            : (transcript.text || '');

        setViewMode('output');
        setSelectedSummary(null);
        generateSummary(
            summary.template_id || '',
            summary.model,
            tpl?.prompt || '',
            transcriptText,
            includeSpeakers
        );
    };

    const handleDeleteSummary = async (summaryId: string) => {
        try {
            const res = await fetch(`/api/v1/transcription/${audioId}/summaries/${summaryId}`, {
                method: 'DELETE',
                headers: { ...getAuthHeaders() },
            });
            if (res.ok) {
                queryClient.invalidateQueries({ queryKey: ["summaries", audioId] });
                queryClient.invalidateQueries({ queryKey: ["summary", audioId] });
                if (selectedSummary?.id === summaryId) {
                    setSelectedSummary(null);
                }
                toast({ title: 'Summary deleted' });
            }
        } catch {
            toast({ title: 'Failed to delete summary' });
        }
    };

    const handleCopy = async (content: string) => {
        if (content) {
            await navigator.clipboard.writeText(content);
            toast({ title: 'Copied to clipboard' });
        }
    };

    const handleDownload = (content: string) => {
        if (!content) return;
        const title = audioFile?.title || "summary";
        const filename = `${title.replace(/[^a-z0-9]/gi, '_').toLowerCase()}-summary.md`;
        const blob = new Blob([content], { type: 'text/markdown' });
        const url = URL.createObjectURL(blob);
        const link = document.createElement('a');
        link.href = url;
        link.download = filename;
        document.body.appendChild(link);
        link.click();
        document.body.removeChild(link);
        URL.revokeObjectURL(url);
    };

    const handleOpenChange = (open: boolean) => {
        if (!open && isStreaming) return;
        onClose(open);
    };

    // ── Output View (streaming or just-generated) ──
    if (viewMode === 'output') {
        const displayContent = streamContent || '';
        return (
            <Dialog open={isOpen} onOpenChange={handleOpenChange}>
                <DialogContent className="w-[calc(100%-2rem)] max-w-4xl mx-auto bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-[var(--shadow-float)] p-0 rounded-[var(--radius-card)] max-h-[85vh] overflow-hidden">
                    <DialogHeader className="p-5 pb-4 border-b border-[var(--border-subtle)]">
                        <DialogTitle className="text-xl font-bold text-[var(--text-primary)] flex items-center gap-2">
                            <div className="h-9 w-9 rounded-[var(--radius-btn)] bg-gradient-to-br from-[var(--brand-start)] to-[var(--brand-solid)] flex items-center justify-center shadow-sm">
                                <Sparkles className="h-4 w-4 text-white" />
                            </div>
                            Summary
                        </DialogTitle>
                        <DialogDescription className="flex items-center gap-2 text-[var(--text-secondary)] mt-1">
                            {isStreaming ? (
                                <>
                                    <span>Generating summary</span>
                                    <span className="flex gap-1">
                                        <span className="w-1.5 h-1.5 bg-[var(--brand-solid)] rounded-full animate-bounce" style={{ animationDelay: '0ms' }} />
                                        <span className="w-1.5 h-1.5 bg-[var(--brand-solid)] rounded-full animate-bounce" style={{ animationDelay: '150ms' }} />
                                        <span className="w-1.5 h-1.5 bg-[var(--brand-solid)] rounded-full animate-bounce" style={{ animationDelay: '300ms' }} />
                                    </span>
                                </>
                            ) : (
                                <span>{error ? 'Generation failed' : 'Summary ready'}</span>
                            )}
                        </DialogDescription>
                    </DialogHeader>

                    <div className="p-5 pt-4">
                        <div className="flex flex-wrap items-center justify-end gap-2 mb-4">
                            {!isStreaming && (
                                <Button
                                    variant="outline"
                                    size="sm"
                                    onClick={() => {
                                        setViewMode(allSummaries.length > 0 ? 'list' : 'template-picker');
                                        queryClient.invalidateQueries({ queryKey: ["summaries", audioId] });
                                        queryClient.invalidateQueries({ queryKey: ["summary", audioId] });
                                    }}
                                    className="h-9 rounded-[var(--radius-btn)] border-[var(--border-subtle)] hover:bg-[var(--bg-muted-pane)] transition-all mr-auto"
                                >
                                    <FileText className="h-3.5 w-3.5" />
                                    All Summaries
                                </Button>
                            )}
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => handleCopy(displayContent)}
                                disabled={!displayContent}
                                className="h-9 rounded-[var(--radius-btn)] border-[var(--border-subtle)] hover:bg-[var(--bg-muted-pane)] transition-all"
                            >
                                <Copy className="h-3.5 w-3.5" />
                                Copy
                            </Button>
                            <Button
                                variant="outline"
                                size="sm"
                                onClick={() => handleDownload(displayContent)}
                                disabled={!displayContent}
                                className="h-9 rounded-[var(--radius-btn)] border-[var(--border-subtle)] hover:bg-[var(--bg-muted-pane)] transition-all"
                            >
                                <Download className="h-3.5 w-3.5" />
                                Download
                            </Button>
                        </div>

                        <div className="min-h-[200px] max-h-[55vh] overflow-y-auto font-reading">
                            {error ? (
                                <p className="text-sm text-[var(--error)]">{error}</p>
                            ) : isStreaming && !displayContent ? (
                                <div className="flex flex-col items-center justify-center py-12 text-[var(--text-tertiary)]">
                                    <div className="relative h-12 w-12 mb-4">
                                        <div className="absolute inset-0 rounded-full border-2 border-[var(--brand-solid)]/20"></div>
                                        <div className="absolute inset-0 rounded-full border-2 border-transparent border-t-[var(--brand-solid)] animate-spin"></div>
                                        <Sparkles className="absolute inset-0 m-auto h-5 w-5 text-[var(--brand-solid)] animate-pulse" />
                                    </div>
                                    <p className="text-sm font-medium">Generating summary...</p>
                                    <p className="text-xs mt-1">This may take a moment</p>
                                </div>
                            ) : (
                                <>
                                    <MarkdownContent content={displayContent} />
                                    {isStreaming && (
                                        <span className="inline-block w-2 h-5 bg-[var(--brand-solid)] ml-0.5 animate-pulse align-middle" />
                                    )}
                                </>
                            )}
                        </div>
                    </div>
                </DialogContent>
            </Dialog>
        );
    }

    // ── Template Picker View ──
    if (viewMode === 'template-picker') {
        return (
            <Dialog open={isOpen} onOpenChange={handleOpenChange}>
                <DialogContent
                    className="w-[calc(100%-2rem)] max-w-lg mx-auto bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-[var(--shadow-float)] p-0 rounded-[var(--radius-card)] overflow-hidden"
                    onPointerDownOutside={(e) => {
                        if (tplPopoverOpen) e.preventDefault();
                    }}
                >
                    <DialogHeader className="p-5 pb-0">
                        <DialogTitle className="text-xl font-bold text-[var(--text-primary)] flex items-center gap-2">
                            <div className="h-9 w-9 rounded-[var(--radius-btn)] bg-gradient-to-br from-[var(--brand-start)] to-[var(--brand-solid)] flex items-center justify-center shadow-sm">
                                <FileText className="h-4 w-4 text-white" />
                            </div>
                            Summarize Transcript
                        </DialogTitle>
                        <DialogDescription className="text-[var(--text-tertiary)] mt-1">
                            Choose a summarization template to generate insights
                        </DialogDescription>
                    </DialogHeader>

                    <div className="p-5 space-y-5">
                        {llmReady === false && (
                            <div className="p-4 bg-[var(--brand-light)] text-[var(--brand-solid)] dark:text-[var(--brand-300)] border border-[var(--brand-solid)]/20 rounded-xl text-sm flex items-center gap-2">
                                <span className="h-2 w-2 bg-[var(--brand-solid)] rounded-full animate-pulse" />
                                LLM is not configured or active. Please check settings.
                            </div>
                        )}

                        <div className="space-y-2">
                            <Label className="text-sm font-medium text-[var(--text-secondary)]">Template</Label>
                            <Popover open={tplPopoverOpen} onOpenChange={setTplPopoverOpen} modal={true}>
                                <PopoverTrigger asChild>
                                    <button
                                        className="w-full h-11 inline-flex justify-between items-center rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-muted-pane)] px-4 text-sm text-[var(--text-primary)] hover:border-[var(--brand-solid)]/50 focus:ring-2 focus:ring-[var(--brand-solid)]/20 transition-all outline-none disabled:opacity-50 shadow-sm"
                                        aria-label="Choose template"
                                        disabled={!llmReady}
                                        type="button"
                                    >
                                        <span className="truncate text-left">{selectedTemplate ? selectedTemplate.name : (templatesLoading ? 'Loading...' : 'Select a template')}</span>
                                        <span className="flex items-center text-xs text-[var(--text-tertiary)] ml-2 shrink-0">
                                            {selectedTemplate?.model ? `(${selectedTemplate.model})` : ''}
                                            <ChevronDown className="ml-2 h-4 w-4 opacity-50" />
                                        </span>
                                    </button>
                                </PopoverTrigger>
                                <PopoverContent
                                    className="w-[var(--radix-popover-trigger-width)] p-1 bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-[var(--shadow-float)] rounded-[var(--radius-card)]"
                                    onOpenAutoFocus={(e) => e.preventDefault()}
                                >
                                    <Command className="bg-transparent">
                                        <CommandInput placeholder="Search templates..." className="border-none focus:ring-0 h-10" />
                                        <CommandList className="max-h-64 overflow-auto p-1">
                                            <CommandEmpty className="py-3 text-center text-xs text-[var(--text-tertiary)]">{templatesLoading ? 'Loading...' : 'No templates found'}</CommandEmpty>
                                            <CommandGroup heading="Templates" className="text-[var(--text-tertiary)]">
                                                {templates.map(t => (
                                                    <CommandItem
                                                        key={t.id}
                                                        value={t.name}
                                                        onSelect={() => { setSelectedTemplateId(t.id); setTplPopoverOpen(false); }}
                                                        className="rounded-lg py-2.5 px-3 aria-selected:bg-[var(--brand-solid)] aria-selected:text-white cursor-pointer transition-colors"
                                                    >
                                                        <div className="flex flex-col w-full">
                                                            <span className="text-sm font-medium">{t.name}</span>
                                                            <span className="text-xs opacity-70">Model: {t.model || '—'}</span>
                                                        </div>
                                                    </CommandItem>
                                                ))}
                                            </CommandGroup>
                                        </CommandList>
                                    </Command>
                                </PopoverContent>
                            </Popover>

                            {!templatesLoading && templates.length === 0 && (
                                <p className="text-xs text-[var(--text-tertiary)] pl-1">No templates found. Go to Settings → Summary to create one.</p>
                            )}
                            {selectedTemplate && !selectedTemplate.model && (
                                <p className="text-xs text-[var(--error)] pl-1">Selected template has no model configured.</p>
                            )}
                        </div>
                    </div>

                    <div className="p-5 pt-0 flex flex-col-reverse sm:flex-row gap-3 sm:justify-between">
                        <div>
                            {allSummaries.length > 0 && (
                                <Button
                                    variant="ghost"
                                    onClick={() => {
                                        setViewMode('list');
                                        setSelectedSummary(allSummaries[0]);
                                    }}
                                    className="h-11 px-4 rounded-[var(--radius-btn)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-muted-pane)]"
                                >
                                    <FileText className="h-4 w-4 mr-2" />
                                    View Summaries ({allSummaries.length})
                                </Button>
                            )}
                        </div>
                        <div className="flex gap-3">
                            <Button
                                variant="ghost"
                                onClick={() => onClose(false)}
                                className="h-11 px-6 rounded-[var(--radius-btn)] text-[var(--text-secondary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-muted-pane)]"
                            >
                                Cancel
                            </Button>
                            <Button
                                disabled={!selectedTemplateId || !selectedTemplate?.model || !llmReady}
                                onClick={handleStartSummary}
                                className="h-11 px-6 bg-gradient-to-br from-[var(--brand-start)] to-[var(--brand-end)] text-white hover:brightness-110 active:translate-y-px transition-all shadow-sm disabled:opacity-50 disabled:cursor-not-allowed rounded-[var(--radius-btn)]"
                            >
                                <Sparkles className="h-4 w-4 mr-2" />
                                Generate Summary
                            </Button>
                        </div>
                    </div>
                </DialogContent>
            </Dialog>
        );
    }

    // ── List View (sidebar + content) ──
    const displayedContent = selectedSummary?.content || '';

    return (
        <Dialog open={isOpen} onOpenChange={handleOpenChange}>
            <DialogContent className="w-[calc(100%-2rem)] max-w-5xl mx-auto bg-[var(--bg-card)] border border-[var(--border-subtle)] shadow-[var(--shadow-float)] p-0 rounded-[var(--radius-card)] max-h-[85vh] overflow-hidden">
                <DialogHeader className="p-5 pb-4 border-b border-[var(--border-subtle)]">
                    <div className="flex items-center justify-between">
                        <div>
                            <DialogTitle className="text-xl font-bold text-[var(--text-primary)] flex items-center gap-2">
                                <div className="h-9 w-9 rounded-[var(--radius-btn)] bg-gradient-to-br from-[var(--brand-start)] to-[var(--brand-solid)] flex items-center justify-center shadow-sm">
                                    <Sparkles className="h-4 w-4 text-white" />
                                </div>
                                Summaries
                            </DialogTitle>
                            <DialogDescription className="text-[var(--text-secondary)] mt-1">
                                {allSummaries.length} {allSummaries.length === 1 ? 'summary' : 'summaries'} generated
                            </DialogDescription>
                        </div>
                        <Button
                            size="sm"
                            onClick={() => setViewMode('template-picker')}
                            className="h-9 px-4 bg-gradient-to-br from-[var(--brand-start)] to-[var(--brand-end)] text-white hover:brightness-110 rounded-[var(--radius-btn)] shadow-sm"
                        >
                            <Plus className="h-3.5 w-3.5 mr-1.5" />
                            New Summary
                        </Button>
                    </div>
                </DialogHeader>

                <div className="flex min-h-[400px] max-h-[calc(85vh-100px)]">
                    {/* Sidebar */}
                    <div className="w-64 flex-shrink-0 border-r border-[var(--border-subtle)] overflow-y-auto">
                        {summariesLoading ? (
                            <div className="p-4 space-y-2">
                                {[...Array(3)].map((_, i) => (
                                    <div key={i} className="h-14 rounded-lg bg-[var(--bg-main)] animate-pulse" />
                                ))}
                            </div>
                        ) : (
                            <div className="p-2 space-y-1">
                                {allSummaries.map((s) => (
                                    <button
                                        key={s.id}
                                        onClick={() => setSelectedSummary(s)}
                                        className={`w-full text-left p-3 rounded-lg transition-all group relative ${
                                            selectedSummary?.id === s.id
                                                ? 'bg-[var(--brand-light)] border border-[var(--brand-solid)]/20'
                                                : 'hover:bg-[var(--bg-main)] border border-transparent'
                                        }`}
                                    >
                                        <div className="flex items-start justify-between gap-1">
                                            <div className="min-w-0 flex-1">
                                                <p className="text-sm font-medium text-[var(--text-primary)] truncate">
                                                    {s.template_name || 'Custom'}
                                                </p>
                                                <p className="text-[10px] text-[var(--text-tertiary)] mt-0.5">
                                                    {s.model} · {formatRelativeDate(s.created_at)}
                                                </p>
                                            </div>
                                            <button
                                                onClick={(e) => {
                                                    e.stopPropagation();
                                                    handleDeleteSummary(s.id);
                                                }}
                                                className="opacity-0 group-hover:opacity-100 p-1 rounded hover:bg-[var(--error)]/10 text-[var(--text-tertiary)] hover:text-[var(--error)] transition-all flex-shrink-0"
                                                title="Delete summary"
                                            >
                                                <Trash2 className="h-3 w-3" />
                                            </button>
                                        </div>
                                    </button>
                                ))}
                            </div>
                        )}
                    </div>

                    {/* Content */}
                    <div className="flex-1 min-w-0 flex flex-col">
                        {selectedSummary ? (
                            <>
                                {/* Toolbar */}
                                <div className="flex items-center justify-between px-5 py-3 border-b border-[var(--border-subtle)]">
                                    <div className="text-xs text-[var(--text-tertiary)]">
                                        <span className="font-medium text-[var(--text-secondary)]">{selectedSummary.template_name || 'Custom'}</span>
                                        {' · '}{selectedSummary.model}
                                    </div>
                                    <div className="flex items-center gap-1.5">
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            onClick={() => handleRegenerate(selectedSummary)}
                                            disabled={!transcript}
                                            className="h-8 px-2.5 text-xs rounded-[var(--radius-btn)] hover:bg-[var(--bg-muted-pane)]"
                                            title="Regenerate with same template"
                                        >
                                            <RefreshCw className="h-3 w-3 mr-1" />
                                            Regenerate
                                        </Button>
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            onClick={() => handleCopy(displayedContent)}
                                            className="h-8 px-2.5 text-xs rounded-[var(--radius-btn)] hover:bg-[var(--bg-muted-pane)]"
                                        >
                                            <Copy className="h-3 w-3 mr-1" />
                                            Copy
                                        </Button>
                                        <Button
                                            variant="ghost"
                                            size="sm"
                                            onClick={() => handleDownload(displayedContent)}
                                            className="h-8 px-2.5 text-xs rounded-[var(--radius-btn)] hover:bg-[var(--bg-muted-pane)]"
                                        >
                                            <Download className="h-3 w-3 mr-1" />
                                            Download
                                        </Button>
                                    </div>
                                </div>

                                {/* Summary content */}
                                <div className="flex-1 overflow-y-auto p-5 font-reading">
                                    <MarkdownContent content={displayedContent} />
                                </div>
                            </>
                        ) : (
                            <div className="flex-1 flex items-center justify-center text-[var(--text-tertiary)]">
                                <div className="text-center">
                                    <FileText className="h-10 w-10 mx-auto mb-3 opacity-30" />
                                    <p className="text-sm">Select a summary to view</p>
                                </div>
                            </div>
                        )}
                    </div>
                </div>
            </DialogContent>
        </Dialog>
    );
}
