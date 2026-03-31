import { useState, useEffect, useRef, useCallback } from 'react';

function escapeRegExp(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

interface MatchRange {
  range: Range;
  node: Node;
}

interface UseTranscriptSearchResult {
  matchCount: number;
  activeMatchIndex: number;
  goToNext: () => void;
  goToPrevious: () => void;
  clearHighlights: () => void;
}

export function useTranscriptSearch(
  searchQuery: string,
  containerRef: React.RefObject<HTMLDivElement | null>,
  transcriptMode: string,
): UseTranscriptSearchResult {
  const [matchCount, setMatchCount] = useState(0);
  const [activeMatchIndex, setActiveMatchIndex] = useState(0);
  const matchesRef = useRef<MatchRange[]>([]);

  // Build matches and set CSS highlights
  const runSearch = useCallback(() => {
    // Clear previous highlights
    if (typeof CSS !== 'undefined' && CSS.highlights) {
      CSS.highlights.delete('search-match');
      CSS.highlights.delete('search-match-active');
    }

    const query = searchQuery.trim();
    if (!query || !containerRef.current) {
      matchesRef.current = [];
      setMatchCount(0);
      setActiveMatchIndex(0);
      return;
    }

    const matches: MatchRange[] = [];
    const walker = document.createTreeWalker(
      containerRef.current,
      NodeFilter.SHOW_TEXT,
    );

    const pattern = new RegExp(escapeRegExp(query), 'gi');

    let node: Text | null;
    while ((node = walker.nextNode() as Text | null)) {
      // Only search within transcript text containers
      const parent = node.parentElement;
      if (!parent?.closest('[data-transcript-text]')) continue;

      const text = node.textContent || '';
      let match: RegExpExecArray | null;
      pattern.lastIndex = 0;
      while ((match = pattern.exec(text)) !== null) {
        try {
          const range = new Range();
          range.setStart(node, match.index);
          range.setEnd(node, match.index + match[0].length);
          matches.push({ range, node });
        } catch {
          // Ignore invalid ranges
        }
      }
    }

    matchesRef.current = matches;
    setMatchCount(matches.length);

    if (matches.length === 0) {
      setActiveMatchIndex(0);
      return;
    }

    // Reset to first match
    setActiveMatchIndex(0);

    // Apply highlights
    if (typeof CSS !== 'undefined' && CSS.highlights) {
      const allRanges = matches.map(m => m.range);
      CSS.highlights.set('search-match', new Highlight(...allRanges));
      CSS.highlights.set('search-match-active', new Highlight(matches[0].range));
    }
  }, [searchQuery, containerRef]);

  // Re-run search when query or mode changes (mode change rebuilds DOM)
  useEffect(() => {
    // Use rAF to let React finish rendering new DOM after mode change
    const id = requestAnimationFrame(() => runSearch());
    return () => cancelAnimationFrame(id);
  }, [runSearch, transcriptMode]);

  // Update active highlight when activeMatchIndex changes
  useEffect(() => {
    if (typeof CSS === 'undefined' || !CSS.highlights) return;
    const matches = matchesRef.current;
    if (matches.length === 0) return;

    const active = matches[activeMatchIndex];
    if (!active) return;

    CSS.highlights.set('search-match-active', new Highlight(active.range));

    // Scroll active match into view
    try {
      const parent = active.range.startContainer.parentElement;
      if (parent) {
        parent.scrollIntoView({ behavior: 'smooth', block: 'center' });
      }
    } catch {
      // Ignore scroll errors
    }
  }, [activeMatchIndex]);

  // Cleanup on unmount
  useEffect(() => {
    return () => {
      if (typeof CSS !== 'undefined' && CSS.highlights) {
        CSS.highlights.delete('search-match');
        CSS.highlights.delete('search-match-active');
      }
    };
  }, []);

  const goToNext = useCallback(() => {
    if (matchesRef.current.length === 0) return;
    setActiveMatchIndex(prev => (prev + 1) % matchesRef.current.length);
  }, []);

  const goToPrevious = useCallback(() => {
    if (matchesRef.current.length === 0) return;
    setActiveMatchIndex(prev =>
      prev === 0 ? matchesRef.current.length - 1 : prev - 1
    );
  }, []);

  const clearHighlights = useCallback(() => {
    if (typeof CSS !== 'undefined' && CSS.highlights) {
      CSS.highlights.delete('search-match');
      CSS.highlights.delete('search-match-active');
    }
    matchesRef.current = [];
    setMatchCount(0);
    setActiveMatchIndex(0);
  }, []);

  return {
    matchCount,
    activeMatchIndex,
    goToNext,
    goToPrevious,
    clearHighlights,
  };
}

export { escapeRegExp };
