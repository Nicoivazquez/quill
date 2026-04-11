import { memo } from "react";
import { ArrowUpDown, Users } from "lucide-react";
import {
    Select,
    SelectContent,
    SelectItem,
    SelectTrigger,
    SelectValue,
} from "@/components/ui/select";
import { useSpeakers } from "@/features/transcription/hooks/useSpeakers";

export interface ListFilters {
    status: string;
    speaker: string;
    speakerStatus: string;
    sortBy: string;
    sortOrder: "asc" | "desc";
}

interface ListFilterBarProps {
    filters: ListFilters;
    onFiltersChange: (filters: ListFilters) => void;
}

const STATUS_OPTIONS = [
    { value: "all", label: "All Statuses" },
    { value: "completed", label: "Completed" },
    { value: "processing", label: "Processing" },
    { value: "failed", label: "Failed" },
];

const SORT_OPTIONS = [
    { value: "created_at:desc", label: "Newest First" },
    { value: "created_at:asc", label: "Oldest First" },
    { value: "recorded_at:desc", label: "Recording Date (Newest)" },
    { value: "recorded_at:asc", label: "Recording Date (Oldest)" },
    { value: "title:asc", label: "Title A-Z" },
    { value: "title:desc", label: "Title Z-A" },
    { value: "updated_at:desc", label: "Recently Updated" },
    { value: "status:asc", label: "Status" },
];

export const ListFilterBar = memo(function ListFilterBar({
    filters,
    onFiltersChange,
}: ListFilterBarProps) {
    const { data: speakers = [] } = useSpeakers();

    const update = (partial: Partial<ListFilters>) => {
        onFiltersChange({ ...filters, ...partial });
    };

    const currentSort = `${filters.sortBy}:${filters.sortOrder}`;

    return (
        <div className="flex flex-wrap items-center gap-2">
            {/* Status Filter */}
            <Select
                value={filters.status || "all"}
                onValueChange={(v) => update({ status: v === "all" ? "" : v })}
            >
                <SelectTrigger size="sm" className="text-xs">
                    <SelectValue placeholder="Status" />
                </SelectTrigger>
                <SelectContent>
                    {STATUS_OPTIONS.map((opt) => (
                        <SelectItem key={opt.value} value={opt.value}>
                            {opt.label}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>

            {/* Speaker Filter */}
            {speakers.length > 0 && (
                <Select
                    value={filters.speaker || "all"}
                    onValueChange={(v) => update({ speaker: v === "all" ? "" : v })}
                >
                    <SelectTrigger size="sm" className="text-xs max-w-[160px]">
                        <SelectValue placeholder="Speaker" />
                    </SelectTrigger>
                    <SelectContent>
                        <SelectItem value="all">All Speakers</SelectItem>
                        {speakers.map((s) => (
                            <SelectItem key={s} value={s}>
                                {s}
                            </SelectItem>
                        ))}
                    </SelectContent>
                </Select>
            )}

            {/* Speaker Status Filter */}
            <Select
                value={filters.speakerStatus || "all"}
                onValueChange={(v) => update({ speakerStatus: v === "all" ? "" : v })}
            >
                <SelectTrigger size="sm" className="text-xs">
                    <Users className="h-3 w-3 mr-1 opacity-50" />
                    <SelectValue placeholder="Speakers" />
                </SelectTrigger>
                <SelectContent>
                    <SelectItem value="all">All</SelectItem>
                    <SelectItem value="needs_attention">Needs Attention</SelectItem>
                    <SelectItem value="identified">Identified</SelectItem>
                </SelectContent>
            </Select>

            {/* Sort */}
            <Select
                value={currentSort}
                onValueChange={(v) => {
                    const [sortBy, sortOrder] = v.split(":") as [string, "asc" | "desc"];
                    update({ sortBy, sortOrder });
                }}
            >
                <SelectTrigger size="sm" className="text-xs">
                    <ArrowUpDown className="h-3 w-3 mr-1 opacity-50" />
                    <SelectValue placeholder="Sort" />
                </SelectTrigger>
                <SelectContent>
                    {SORT_OPTIONS.map((opt) => (
                        <SelectItem key={opt.value} value={opt.value}>
                            {opt.label}
                        </SelectItem>
                    ))}
                </SelectContent>
            </Select>
        </div>
    );
});
