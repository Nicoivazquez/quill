package contacts

import (
	"math"
	"sort"

	"quill/pkg/logger"
)

// ContactEmbedding holds a pre-loaded voice embedding for a single contact.
type ContactEmbedding struct {
	ContactID   uint
	ContactName string
	Vector      []float64
}

// SpeakerMatch is the best contact match found for a single speaker label.
type SpeakerMatch struct {
	Speaker     string           // e.g. "speaker_00"
	ContactID   uint
	ContactName string
	Score       float64
	Tier        SpeakerMatchTier // "auto", "suggest", "unknown"
}

// SpeakerIdentificationResult is the output of MatchSpeakers.
type SpeakerIdentificationResult struct {
	Matches   []SpeakerMatch // Best match per speaker (only above TierUnknown threshold)
	Unmatched []string       // Speakers with no match at or above 0.35
}

// MatchSpeakers compares each speaker's embedding against all contact
// embeddings and returns the best match per speaker.
//
// Rules:
//  1. For each speaker, compute cosine similarity against every contact.
//  2. Pick the highest-scoring contact.  If the score is below 0.35 (TierUnknown),
//     the speaker has no match and goes to Unmatched.
//  3. Conflict resolution: if two speakers both claim the same contact as their
//     best match, only the speaker with the higher score keeps the match; the
//     other speaker is moved to Unmatched.
//
// The function never panics on nil or empty inputs.
func MatchSpeakers(speakerEmbeddings map[string][]float64, contactEmbeddings []ContactEmbedding) *SpeakerIdentificationResult {
	result := &SpeakerIdentificationResult{
		Matches:   []SpeakerMatch{},
		Unmatched: []string{},
	}

	if len(speakerEmbeddings) == 0 {
		return result
	}

	// Step 1: find the best contact for each speaker.
	type candidate struct {
		speaker string
		match   SpeakerMatch
	}

	candidates := make([]candidate, 0, len(speakerEmbeddings))

	// Process speakers in deterministic order so tie-breaking is stable.
	speakerKeys := make([]string, 0, len(speakerEmbeddings))
	for k := range speakerEmbeddings {
		speakerKeys = append(speakerKeys, k)
	}
	sort.Strings(speakerKeys)

	for _, speaker := range speakerKeys {
		vec := speakerEmbeddings[speaker]

		// Log speaker embedding magnitude for diagnostics.
		var speakerMag float64
		for _, v := range vec {
			speakerMag += v * v
		}
		speakerMag = math.Sqrt(speakerMag)
		logger.Debug("speaker-match: speaker embedding info",
			"speaker", speaker, "dim", len(vec), "magnitude", speakerMag)

		if len(contactEmbeddings) == 0 {
			result.Unmatched = append(result.Unmatched, speaker)
			continue
		}

		var bestScore float64 = -2.0 // below any possible cosine value
		var bestContact *ContactEmbedding

		for i := range contactEmbeddings {
			// Log contact embedding magnitude once per contact per run.
			var contactMag float64
			for _, v := range contactEmbeddings[i].Vector {
				contactMag += v * v
			}
			contactMag = math.Sqrt(contactMag)

			score := CosineSimilarity(vec, contactEmbeddings[i].Vector)
			logger.Debug("speaker-match: cosine similarity",
				"speaker", speaker, "speaker_mag", speakerMag,
				"contact", contactEmbeddings[i].ContactName, "contact_mag", contactMag,
				"score", score)
			if score > bestScore {
				bestScore = score
				bestContact = &contactEmbeddings[i]
			}
		}

		logger.Debug("speaker-match: best match",
			"speaker", speaker, "best_contact", bestContact.ContactName,
			"best_score", bestScore, "tier", ClassifySpeakerMatch(bestScore))

		tier := ClassifySpeakerMatch(bestScore)
		if tier == TierUnknown {
			result.Unmatched = append(result.Unmatched, speaker)
			continue
		}

		candidates = append(candidates, candidate{
			speaker: speaker,
			match: SpeakerMatch{
				Speaker:     speaker,
				ContactID:   bestContact.ContactID,
				ContactName: bestContact.ContactName,
				Score:       bestScore,
				Tier:        tier,
			},
		})
	}

	// Step 2: resolve conflicts — each contact may only be claimed by one
	// speaker (the one with the highest score).
	//
	// Build a map: contactID → best candidate so far.
	winner := make(map[uint]candidate, len(candidates))
	loserSpeakers := make([]string, 0)

	// Sort candidates by descending score so the highest score always wins when
	// two speakers target the same contact.
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].match.Score > candidates[j].match.Score
	})

	for _, c := range candidates {
		cid := c.match.ContactID
		if existing, claimed := winner[cid]; !claimed {
			winner[cid] = c
		} else {
			// Current candidate lost because winner has a higher or equal score.
			// (Equal scores: the first one in sorted order wins — stable enough.)
			_ = existing
			loserSpeakers = append(loserSpeakers, c.speaker)
		}
	}

	for _, c := range winner {
		result.Matches = append(result.Matches, c.match)
	}
	result.Unmatched = append(result.Unmatched, loserSpeakers...)

	// Sort Matches and Unmatched for deterministic output.
	sort.Slice(result.Matches, func(i, j int) bool {
		return result.Matches[i].Speaker < result.Matches[j].Speaker
	})
	sort.Strings(result.Unmatched)

	return result
}
