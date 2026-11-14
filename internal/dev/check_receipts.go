package dev

// currentReceipts evaluates one consistent view for all checks. The working tree is recorded at most once per
// view, not once per historical receipt or per row in the terminal panel.
func (g *Gate) currentReceipts() map[string]*CheckReceipt {
	checks := g.checks()
	pointers := make([]*Check, len(checks))
	for index := range checks {
		pointers[index] = &checks[index]
	}
	return g.receiptsFor(pointers)
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
	var tree string
	var measured bool
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
			tree, _ = g.snapshot()
		}
		if tree != "" && receipt.Tree == tree {
			current[id] = receipt
		}
	}
	return current
}
