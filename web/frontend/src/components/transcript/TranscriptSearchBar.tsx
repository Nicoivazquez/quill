import { useRef, useEffect } from 'react';
import { Search, ChevronUp, ChevronDown, X } from 'lucide-react';

interface TranscriptSearchBarProps {
  query: string;
  onQueryChange: (q: string) => void;
  matchCount: number;
  activeMatchIndex: number;
  onNext: () => void;
  onPrevious: () => void;
  onClose: () => void;
}

export function TranscriptSearchBar({
  query,
  onQueryChange,
  matchCount,
  activeMatchIndex,
  onNext,
  onPrevious,
  onClose,
}: TranscriptSearchBarProps) {
  const inputRef = useRef<HTMLInputElement>(null);

  // Auto-focus on mount
  useEffect(() => {
    inputRef.current?.focus();
  }, []);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.preventDefault();
      onClose();
    } else if (e.key === 'Enter' && e.shiftKey) {
      e.preventDefault();
      onPrevious();
    } else if (e.key === 'Enter') {
      e.preventDefault();
      onNext();
    }
  };

  const hasQuery = query.trim().length > 0;

  return (
    <div className="flex items-center gap-2 px-3 py-2 mb-3 rounded-[var(--radius-btn)] border border-[var(--border-subtle)] bg-[var(--bg-card)] shadow-sm">
      <Search className="h-4 w-4 text-[var(--text-tertiary)] flex-shrink-0" />
      <input
        ref={inputRef}
        type="text"
        value={query}
        onChange={e => onQueryChange(e.target.value)}
        onKeyDown={handleKeyDown}
        placeholder="Search transcript..."
        className="flex-1 min-w-0 bg-transparent text-sm text-[var(--text-primary)] placeholder:text-[var(--text-tertiary)] outline-none"
      />
      {hasQuery && (
        <span className="text-xs text-[var(--text-tertiary)] whitespace-nowrap select-none">
          {matchCount === 0
            ? 'No results'
            : `${activeMatchIndex + 1} of ${matchCount}`}
        </span>
      )}
      <div className="flex items-center gap-0.5">
        <button
          type="button"
          onClick={onPrevious}
          disabled={matchCount === 0}
          className="h-6 w-6 inline-flex items-center justify-center rounded text-[var(--text-tertiary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-muted-pane)] disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
          aria-label="Previous match"
        >
          <ChevronUp className="h-3.5 w-3.5" />
        </button>
        <button
          type="button"
          onClick={onNext}
          disabled={matchCount === 0}
          className="h-6 w-6 inline-flex items-center justify-center rounded text-[var(--text-tertiary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-muted-pane)] disabled:opacity-30 disabled:cursor-not-allowed transition-colors"
          aria-label="Next match"
        >
          <ChevronDown className="h-3.5 w-3.5" />
        </button>
      </div>
      <button
        type="button"
        onClick={onClose}
        className="h-6 w-6 inline-flex items-center justify-center rounded text-[var(--text-tertiary)] hover:text-[var(--text-primary)] hover:bg-[var(--bg-muted-pane)] transition-colors"
        aria-label="Close search"
      >
        <X className="h-3.5 w-3.5" />
      </button>
    </div>
  );
}
