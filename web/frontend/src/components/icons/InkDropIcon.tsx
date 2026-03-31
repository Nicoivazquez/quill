import { cn } from "@/lib/utils";

interface InkDropIconProps {
	className?: string;
	/** Number to display inside the drop */
	count?: number;
}

/**
 * Filled ink drop — the speaker's "ink" is known.
 * Used for identified/renamed speakers.
 */
export function InkDropFilled({ className, count }: InkDropIconProps) {
	return (
		<svg
			viewBox="0 0 20 24"
			fill="currentColor"
			className={cn("inline-block", className)}
			aria-hidden="true"
		>
			{/* Drop shape */}
			<path d="M10 1C10 1 2 10.5 2 15.5C2 19.9 5.6 23 10 23C14.4 23 18 19.9 18 15.5C18 10.5 10 1 10 1Z" />
			{count !== undefined && (
				<text
					x="10"
					y="17"
					textAnchor="middle"
					dominantBaseline="central"
					fill="white"
					fontSize="9"
					fontWeight="700"
					fontFamily="system-ui, sans-serif"
				>
					{count}
				</text>
			)}
		</svg>
	);
}

/**
 * Outlined/empty ink drop — waiting to be filled.
 * Used for unidentified speakers.
 */
export function InkDropEmpty({ className, count }: InkDropIconProps) {
	return (
		<svg
			viewBox="0 0 20 24"
			fill="none"
			stroke="currentColor"
			strokeWidth="1.5"
			className={cn("inline-block", className)}
			aria-hidden="true"
		>
			{/* Drop outline */}
			<path d="M10 1.5C10 1.5 2.5 10.5 2.5 15.5C2.5 19.6 5.8 22.5 10 22.5C14.2 22.5 17.5 19.6 17.5 15.5C17.5 10.5 10 1.5 10 1.5Z" />
			{count !== undefined && (
				<text
					x="10"
					y="17"
					textAnchor="middle"
					dominantBaseline="central"
					fill="currentColor"
					stroke="none"
					fontSize="9"
					fontWeight="700"
					fontFamily="system-ui, sans-serif"
				>
					{count}
				</text>
			)}
		</svg>
	);
}

/**
 * Half-filled ink drop with sparkle — the quill is guessing.
 * Used for pending speaker suggestions.
 */
export function InkDropSuggestion({ className, count }: InkDropIconProps) {
	return (
		<svg
			viewBox="0 0 24 28"
			fill="none"
			className={cn("inline-block", className)}
			aria-hidden="true"
		>
			{/* Drop outline */}
			<path
				d="M12 2C12 2 4 12 4 17.5C4 22.2 7.6 25.5 12 25.5C16.4 25.5 20 22.2 20 17.5C20 12 12 2 12 2Z"
				stroke="currentColor"
				strokeWidth="1.5"
			/>
			{/* Half fill — bottom portion */}
			<clipPath id="halfFill">
				<path d="M12 2C12 2 4 12 4 17.5C4 22.2 7.6 25.5 12 25.5C16.4 25.5 20 22.2 20 17.5C20 12 12 2 12 2Z" />
			</clipPath>
			<rect
				x="4"
				y="17"
				width="16"
				height="9"
				fill="currentColor"
				opacity="0.35"
				clipPath="url(#halfFill)"
			/>
			{/* Sparkle — small 4-point star at top right */}
			<path
				d="M19 4L19.7 6.3L22 7L19.7 7.7L19 10L18.3 7.7L16 7L18.3 6.3L19 4Z"
				fill="currentColor"
				opacity="0.7"
			/>
			{count !== undefined && (
				<text
					x="12"
					y="19.5"
					textAnchor="middle"
					dominantBaseline="central"
					fill="currentColor"
					fontSize="9"
					fontWeight="700"
					fontFamily="system-ui, sans-serif"
				>
					{count}
				</text>
			)}
		</svg>
	);
}
