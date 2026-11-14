package dev

// currentReceipts evaluates one consistent view for all checks. Whole-task and
// repository fingerprints are computed at most once per view, not once per
// historical receipt or per row in the terminal panel.
func (g *Gate) currentReceipts() map[string]*CheckReceipt {
	checks := make([]*Check, len(g.Plan.Checks))
	for index := range g.Plan.Checks {
		checks[index] = &g.Plan.Checks[index]
	}
	return g.receiptsFor(checks)
}

func (g *Gate) currentReceipt(check *Check) *CheckReceipt {
	return g.receiptsFor([]*Check{check})[checkID(check.Argv)]
}

func (g *Gate) receiptsFor(checks []*Check) map[string]*CheckReceipt {
	latest := make(map[string]*CheckReceipt, len(g.Checks))
	for index := range g.Checks {
		receipt := &g.Checks[index]
		latest[checkID(receipt.Argv)] = receipt
	}
	current := make(map[string]*CheckReceipt, len(checks))
	var content, repository string
	var measured, valid bool
	for _, check := range checks {
		id := checkID(check.Argv)
		receipt := latest[id]
		if receipt == nil {
			continue
		}
		if len(check.Inputs) > 0 {
			key, err := inputKey(check)
			if err == nil && receipt.InputKey == key {
				current[id] = receipt
			}
			continue
		}
		if !measured {
			measured = true
			var contentErr, repositoryErr error
			content, _, contentErr = g.contentDigest()
			repository, repositoryErr = repositoryDigest()
			valid = contentErr == nil && repositoryErr == nil
		}
		if valid && receipt.Repository != "" && receipt.Repository == repository && receipt.Digest == content {
			current[id] = receipt
		}
	}
	return current
}
