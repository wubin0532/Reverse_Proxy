export function calculateTrafficRates(current = {}, previous = {}, elapsedSeconds = 0) {
  const result = {}
  const rate = (value = 0, old = 0) => elapsedSeconds > 0 && value >= old ? (value - old) / elapsedSeconds : 0
  for (const [siteID, site] of Object.entries(current)) {
    const oldSite = previous?.[siteID]
    result[siteID] = {
      bytesIn: rate(site.bytesIn, oldSite?.bytesIn),
      bytesOut: rate(site.bytesOut, oldSite?.bytesOut),
      rules: {}
    }
    for (const [ruleID, rule] of Object.entries(site.rules || {})) {
      const oldRule = oldSite?.rules?.[ruleID]
      result[siteID].rules[ruleID] = {
        bytesIn: rate(rule.bytesIn, oldRule?.bytesIn),
        bytesOut: rate(rule.bytesOut, oldRule?.bytesOut)
      }
    }
  }
  return result
}
