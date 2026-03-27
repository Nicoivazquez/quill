/**
 * Converts a numeric speaker index (0, 1, 2, …) to a human-friendly label
 * ("Speaker A", "Speaker B", "Speaker C", …). Uses base-26 alphabetic conversion
 * so indices beyond 25 produce labels like "Speaker AA", "Speaker AB", etc.
 */
export function speakerIndexToLabel(index: number): string {
    let n = index + 1;
    let label = "";
    while (n > 0) {
        const rem = (n - 1) % 26;
        label = String.fromCharCode(65 + rem) + label;
        n = Math.floor((n - 1) / 26);
    }
    return `Speaker ${label}`;
}

/**
 * Converts a raw speaker key like "speaker_0", "SPEAKER_2", or "Speaker 1"
 * into a human-friendly label like "Speaker A", "Speaker B", etc.
 *
 * Returns the original value unchanged if it doesn't match a known pattern.
 */
export function formatSpeakerLabel(rawSpeaker: string): string {
    // Match patterns like "speaker_0", "SPEAKER_2", "speaker 1"
    const match = rawSpeaker.match(/^speaker[_ ](\d+)$/i);
    if (match) {
        return speakerIndexToLabel(parseInt(match[1], 10));
    }
    return rawSpeaker;
}
