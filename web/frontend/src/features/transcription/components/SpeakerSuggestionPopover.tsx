import { useState } from "react";
import { Check, Loader2, Sparkles, X } from "lucide-react";
import { InkDropSuggestion } from "@/components/icons/InkDropIcon";
import {
	Popover,
	PopoverContent,
	PopoverTrigger,
} from "@/components/ui/popover";
import {
	Tooltip,
	TooltipContent,
	TooltipTrigger,
} from "@/components/ui/tooltip";
import { Button } from "@/components/ui/button";
import { useToast } from "@/components/ui/toast";
import {
	useSpeakerSuggestions,
	usePromoteSpeakerSuggestion,
	useDismissSpeakerSuggestion,
} from "@/features/transcription/hooks/useTranscriptionSpeakers";
import { formatSpeakerLabel } from "@/lib/speaker-utils";

interface SpeakerSuggestionPopoverProps {
	jobId: string;
	count: number;
}

export function SpeakerSuggestionPopover({
	jobId,
	count,
}: SpeakerSuggestionPopoverProps) {
	const [open, setOpen] = useState(false);
	const { toast } = useToast();

	// Lazily fetch suggestions only when the popover is open
	const { data: suggestions = [], isLoading } = useSpeakerSuggestions(
		jobId,
		open,
	);

	const promoteMutation = usePromoteSpeakerSuggestion();
	const dismissMutation = useDismissSpeakerSuggestion();

	// Close popover when all suggestions are handled
	const remainingSuggestions = suggestions.length;

	return (
		<Popover open={open} onOpenChange={setOpen}>
			<Tooltip>
				<TooltipTrigger asChild>
					<PopoverTrigger asChild>
						<button
							className="flex-shrink-0 inline-flex items-center text-amber-600 dark:text-amber-400 cursor-pointer hover:text-amber-700 dark:hover:text-amber-300 transition-colors animate-in fade-in duration-300"
							onClick={(e) => e.stopPropagation()}
						>
							<InkDropSuggestion className="w-5 h-6" count={count} />
						</button>
					</PopoverTrigger>
				</TooltipTrigger>
				<TooltipContent>
					{count === 1
						? "1 speaker needs identification"
						: `${count} speakers need identification`}
				</TooltipContent>
			</Tooltip>

			<PopoverContent
				className="w-72 p-0"
				align="start"
				onClick={(e) => e.stopPropagation()}
			>
				<div className="px-3 py-2 border-b border-border">
					<p className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
						Speaker Suggestions
					</p>
				</div>

				<div className="max-h-64 overflow-y-auto">
					{isLoading ? (
						<div className="flex items-center justify-center py-6">
							<Loader2 className="h-4 w-4 animate-spin text-muted-foreground" />
						</div>
					) : remainingSuggestions === 0 ? (
						<div className="px-3 py-4 text-center text-sm text-muted-foreground">
							No pending suggestions
						</div>
					) : (
						<div className="divide-y divide-border">
							{suggestions.map((suggestion) => {
								const speaker = suggestion.original_speaker;
								const isPromoting =
									promoteMutation.isPending &&
									promoteMutation.variables?.originalSpeaker === speaker;
								const isDismissing =
									dismissMutation.isPending &&
									dismissMutation.variables?.mappingId === suggestion.id;

								return (
									<div key={speaker} className="px-3 py-2.5">
										<div className="flex items-center gap-2 mb-1">
											<span className="text-xs text-muted-foreground">
												{formatSpeakerLabel(speaker)}
											</span>
											<span className="text-xs px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-600 dark:text-amber-400">
												{Math.round(
													(suggestion.confidence_score ?? 0) * 100,
												)}
												% match
											</span>
										</div>
										<div className="flex items-center gap-2">
											<Sparkles className="h-4 w-4 text-amber-500 shrink-0" />
											<span className="text-sm font-medium flex-1 truncate">
												Is this{" "}
												<span className="text-amber-600 dark:text-amber-400">
													{suggestion.custom_name}
												</span>
												?
											</span>
											<div className="flex items-center gap-1 shrink-0">
												<Button
													variant="ghost"
													size="sm"
													className="h-7 px-2 text-green-600 hover:text-green-700 hover:bg-green-500/10"
													disabled={isPromoting || isDismissing}
													onClick={() => {
														promoteMutation.mutate(
															{
																transcriptionId: jobId,
																originalSpeaker: speaker,
																contactId: suggestion.contact_id!,
																contactName: suggestion.custom_name,
																score: suggestion.confidence_score ?? 0,
															},
															{
																onSuccess: () => {
																	toast({
																		title: `Speaker assigned to ${suggestion.custom_name}`,
																		description: `Voice match with ${Math.round((suggestion.confidence_score ?? 0) * 100)}% confidence`,
																	});
																	// Close popover if this was the last one
																	if (remainingSuggestions <= 1) {
																		setOpen(false);
																	}
																},
															},
														);
													}}
												>
													{isPromoting ? (
														<Loader2 className="h-3.5 w-3.5 animate-spin" />
													) : (
														<Check className="h-3.5 w-3.5" />
													)}
													<span className="ml-1">Yes</span>
												</Button>
												<Button
													variant="ghost"
													size="sm"
													className="h-7 px-2 text-muted-foreground hover:text-red-600 hover:bg-red-500/10"
													disabled={isPromoting || isDismissing}
													onClick={() => {
														if (!suggestion.id) return;
														dismissMutation.mutate(
															{
																transcriptionId: jobId,
																mappingId: suggestion.id,
															},
															{
																onSuccess: () => {
																	toast({
																		title: `Suggestion dismissed for ${formatSpeakerLabel(speaker)}`,
																	});
																	if (remainingSuggestions <= 1) {
																		setOpen(false);
																	}
																},
															},
														);
													}}
												>
													{isDismissing ? (
														<Loader2 className="h-3.5 w-3.5 animate-spin" />
													) : (
														<X className="h-3.5 w-3.5" />
													)}
													<span className="ml-1">No</span>
												</Button>
											</div>
										</div>
									</div>
								);
							})}
						</div>
					)}
				</div>
			</PopoverContent>
		</Popover>
	);
}
