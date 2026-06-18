package check

// ---------------------------------------------------------------------------
// Fuzzy matching — "did you mean?" suggestions
// ---------------------------------------------------------------------------

// levenshtein computes the Levenshtein (edit) distance between two strings.
// This is a classic dynamic-programming implementation using O(min(m,n)) space.
func levenshtein(a, b string) int {
        la, lb := len(a), len(b)
        if la == 0 {
                return lb
        }
        if lb == 0 {
                return la
        }

        // Use the shorter string for the column dimension to save memory.
        if la > lb {
                a, b = b, a
                la, lb = lb, la
        }

        // Two-row DP approach.
        prevRow := make([]int, la+1)
        for i := 0; i <= la; i++ {
                prevRow[i] = i
        }

        for j := 1; j <= lb; j++ {
                currRow := make([]int, la+1)
                currRow[0] = j
                bj := b[j-1]
                for i := 1; i <= la; i++ {
                        cost := 1
                        if a[i-1] == bj {
                                cost = 0
                        }
                        del := prevRow[i] + 1       // deletion
                        ins := currRow[i-1] + 1     // insertion
                        sub := prevRow[i-1] + cost   // substitution
                        // Minimum of three
                        m := del
                        if ins < m {
                                m = ins
                        }
                        if sub < m {
                                m = sub
                        }
                        currRow[i] = m
                }
                prevRow = currRow
        }

        return prevRow[la]
}

// suggestSimilar finds the closest match to input among candidates using
// Levenshtein distance. It returns the best matching candidate and its
// distance. If no candidate is within the threshold, returns ("", 0).
//
// Threshold: distance must be <= max(len(input), len(candidate)) / 2
// AND distance must be <= 3.
func suggestSimilar(input string, candidates []string) (string, int) {
        if len(candidates) == 0 || input == "" {
                return "", 0
        }

        best := ""
        bestDist := 0
        inputLen := len(input)

        for _, c := range candidates {
                if c == input {
                        continue // skip exact match
                }
                cLen := len(c)
                d := levenshtein(input, c)

                // Apply threshold: distance <= max(len(input), len(candidate))/2
                maxLen := inputLen
                if cLen > maxLen {
                        maxLen = cLen
                }
                threshold := maxLen / 2
                if threshold > 3 {
                        threshold = 3
                }

                if d > threshold {
                        continue
                }

                // Prefer shorter distances. Tie-break: prefer shorter candidate length.
                if best == "" || d < bestDist || (d == bestDist && cLen < len(best)) {
                        best = c
                        bestDist = d
                }
        }

        return best, bestDist
}

// uniqueStrings deduplicates a slice of strings while preserving order.
func uniqueStrings(xs []string) []string {
        seen := make(map[string]struct{}, len(xs))
        out := make([]string, 0, len(xs))
        for _, x := range xs {
                if _, ok := seen[x]; !ok {
                        seen[x] = struct{}{}
                        out = append(out, x)
                }
        }
        return out
}
