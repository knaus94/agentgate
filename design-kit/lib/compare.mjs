// Design against implementation: which layer is which element, and where they differ.
//
// Nobody marks the code up for this. A text layer is found by its words (the idea of the Design2Code metric), a
// painted layer by where it lies. Both sides are reduced to the same few numbers and compared with tolerances
// (the idea of asdesigned): a missing value on one side is not a difference, and a run that compared nothing fails.

export const defaults = { position: 2, size: 2, radius: 1, fontSize: 0.5, lineHeight: 1, letterSpacing: 0.3, colour: 12, match: 0.5, reach: 300 };

const words = (text) => text.replace(/\s+/g, ' ').trim().toLowerCase();
const centre = ([x, y, width, height]) => [x + width / 2, y + height / 2];

function overlap(a, b) {
  const width = Math.min(a[0] + a[2], b[0] + b[2]) - Math.max(a[0], b[0]);
  const height = Math.min(a[1] + a[3], b[1] + b[3]) - Math.max(a[1], b[1]);
  if (width <= 0 || height <= 0) return 0;
  return (width * height) / (a[2] * a[3] + b[2] * b[3] - width * height);
}

const rgb = (hex) => [1, 3, 5].map((start) => parseInt(hex.slice(start, start + 2), 16));
const colourGap = (hex, actual) => Math.max(...rgb(hex).map((channel, index) => Math.abs(channel - [actual.r, actual.g, actual.b][index])));
const hexOf = ({ r, g, b }) => '#' + [r, g, b].map((channel) => Math.round(channel).toString(16).padStart(2, '0')).join('').toUpperCase();
const signed = (value) => `${value > 0 ? '+' : ''}${Math.round(value * 10) / 10}`;

export function compare(spec, page, map = {}, limits = defaults) {
  const byId = new Map(spec.nodes.map((node) => [node.id, node]));
  const children = new Map();
  for (const node of spec.nodes) {
    if (!children.has(node.parent)) children.set(node.parent, []);
    children.get(node.parent).push(node);
  }
  const ignored = map.ignore || {};
  const dynamic = new Set(map.dynamic || []);
  const hidden = (node) => {
    for (let id = node.id; id; id = byId.get(id)?.parent) {
      if (ignored[byId.get(id)?.ref]) return true;
    }
    return false;
  };
  const nodes = spec.nodes.filter((node) => !hidden(node));
  const skipped = { ignored: spec.nodes.length - nodes.length, unpainted: 0 };
  const anchor = (node) => (node.text.align === 'center' ? 0.5 : node.text.align === 'right' ? 1 : 0);
  const textShift = (node, actual) => [actual.box[0] + actual.box[2] * anchor(node) - (node.box[0] + node.box[2] * anchor(node)),
    centre(actual.box)[1] - centre(node.box)[1]];

  // 1. Texts are found by their words; among equal ones, the nearest to where the design puts it.
  const found = new Map(); // layer id -> { element, shift, sameWords }
  const usedText = new Set();
  const nearest = (node, at, accept) => page.texts.map((text, index) => ({ text, index }))
    .filter(({ text, index }) => !usedText.has(index) && accept(text))
    .sort((a, b) => Math.hypot(...centre(a.text.box).map((value, axis) => value - at[axis])) - Math.hypot(...centre(b.text.box).map((value, axis) => value - at[axis])))[0];
  for (const node of nodes.filter((value) => value.text)) {
    // The same words far away are another element: a screen repeats "USDT" in a tile and in a field.
    const hit = nearest(node, centre(node.box), (text) => words(text.content) === words(node.text.content) &&
      Math.hypot(centre(text.box)[0] - centre(node.box)[0], centre(text.box)[1] - centre(node.box)[1]) <= limits.reach);
    if (!hit) continue;
    usedText.add(hit.index);
    found.set(node.id, { element: hit.text, shift: textShift(node, hit.text), sameWords: true });
  }

  // 2. A painted layer is found by where it lies. A panel that moved took its texts along, so the place to
  // look is the design's place moved the way its found ancestor, or else its own found texts, moved.
  const inside = (node) => (children.get(node.id) || []).flatMap((child) => [child, ...inside(child)]);
  const median = (values) => values.sort((a, b) => a - b)[Math.floor(values.length / 2)];
  const carried = (node) => {
    for (let id = node.parent; id; id = byId.get(id)?.parent) {
      if (found.has(id) && !byId.get(id).text) return found.get(id).shift;
    }
    return null;
  };
  for (const node of nodes) {
    if (node.text) continue;
    if (!(node.asset || (node.fills || []).length > 0 || node.stroke)) {
      skipped.unpainted++;
      continue;
    }
    const texts = inside(node).filter((child) => found.has(child.id)).map((child) => found.get(child.id).shift);
    const guess = carried(node) || (texts.length ? [median(texts.map((shift) => shift[0])), median(texts.map((shift) => shift[1]))] : [0, 0]);
    const expected = [node.box[0] + guess[0], node.box[1] + guess[1], node.box[2], node.box[3]];
    const chosen = map.select?.[node.ref];
    let best = null;
    let score = 0;
    for (const element of page.boxes) {
      if (chosen ? element.where !== chosen : !element.painted) continue;
      // A panel that lost or gained content keeps its corner and one of its sides: that is still the panel.
      const pinned = node.layout && Math.min(node.box[2], node.box[3]) >= 100 && Math.abs(element.box[0] - expected[0]) <= 4 && Math.abs(element.box[1] - expected[1]) <= 4 &&
        (Math.abs(element.box[2] - expected[2]) <= 4 || Math.abs(element.box[3] - expected[3]) <= 4);
      const value = Math.max(overlap(expected, element.box), pinned ? limits.match : 0);
      if (value > score) [best, score] = [element, value];
    }
    if (best && score >= limits.match) found.set(node.id, { element: best, shift: [best.box[0] - node.box[0], best.box[1] - node.box[1]] });
  }

  // 3. A text whose words are not on the page may still be there with live data in it: same place, same size.
  for (const node of nodes.filter((value) => value.text && !found.has(value.id))) {
    const moved = carried(node) || [0, 0];
    const at = centre(node.box).map((value, axis) => value + moved[axis]);
    const hit = nearest(node, at, (text) => text.kind === 'run' && Math.abs(text.size - node.text.size) <= limits.fontSize &&
      Math.hypot(centre(text.box)[0] - at[0], centre(text.box)[1] - at[1]) <= Math.max(8, node.text.size));
    if (!hit) continue;
    usedText.add(hit.index);
    found.set(node.id, { element: hit.text, shift: textShift(node, hit.text), sameWords: false });
  }

  // 4. Differences. Position is judged against the found ancestor, so one moved panel is one finding.
  const issues = [];
  const report = (node, element, kind, text, weight, box) => issues.push({ id: node.id, ref: node.ref,
    layer: node.text ? JSON.stringify(node.text.content.slice(0, 40)) : node.name, element: element?.where, kind, text, weight, box: box || element?.box });
  for (const node of nodes) {
    const match = found.get(node.id);
    if (!match) {
      if (node.text || node.asset || (node.fills || []).length > 0 || node.stroke) {
        const moved = carried(node) || [0, 0];
        report(node, null, 'missing', 'not on the page', 90, [node.box[0] + moved[0], node.box[1] + moved[1], node.box[2], node.box[3]]);
      }
      continue;
    }
    const actual = match.element;
    const base = carried(node) || [0, 0];
    const off = [match.shift[0] - base[0], match.shift[1] - base[1]];
    if (Math.abs(off[0]) > limits.position || Math.abs(off[1]) > limits.position) {
      report(node, actual, 'position', `off by x ${signed(off[0])} y ${signed(off[1])}`, 40 + Math.max(...off.map(Math.abs)));
    }
    if (node.text) {
      const design = node.text;
      if (!match.sameWords && !dynamic.has(node.ref)) report(node, actual, 'text', `reads ${JSON.stringify(actual.content.slice(0, 40))}`, 5);
      if (Math.abs(actual.size - design.size) > limits.fontSize) report(node, actual, 'font', `font-size ${design.size} → ${actual.size}`, 60);
      if (typeof actual.weight === 'number' && design.weight && actual.weight !== design.weight) report(node, actual, 'font', `font-weight ${design.weight} → ${actual.weight}`, 60);
      if (design.family && words(actual.rendered) !== words(design.family)) report(node, actual, 'font', `font ${design.family} → drawn with ${actual.rendered}`, 70);
      if (design.lineHeight && actual.lineHeight && Math.abs(actual.lineHeight - design.lineHeight) > limits.lineHeight) report(node, actual, 'font', `line-height ${design.lineHeight} → ${actual.lineHeight}`, 30);
      if (Math.abs((actual.letterSpacing || 0) - (design.letterSpacing || 0)) > limits.letterSpacing) report(node, actual, 'font', `letter-spacing ${design.letterSpacing} → ${actual.letterSpacing}`, 20);
      const fill = (node.fills || []).find((value) => value.color);
      if (fill && actual.color && !design.runs && colourGap(fill.color, actual.color) > limits.colour) report(node, actual, 'colour', `colour ${fill.color} → ${hexOf(actual.color)}`, 35);
      continue;
    }
    const grow = [actual.box[2] - node.box[2], actual.box[3] - node.box[3]];
    if (Math.abs(grow[0]) > limits.size || Math.abs(grow[1]) > limits.size) {
      report(node, actual, 'size', `${node.box[2]}×${node.box[3]} → ${actual.box[2]}×${actual.box[3]}`, 40 + Math.max(...grow.map(Math.abs)));
    }
    if (node.asset) continue;
    if (node.radius !== undefined) {
      const design = [].concat(node.radius).length === 4 ? node.radius : Array(4).fill(node.radius);
      const round = Math.min(actual.box[2], actual.box[3]) / 2; // past half the side every radius draws the same
      const differs = design.some((value, index) => typeof actual.radius[index] === 'number' &&
        Math.abs(Math.min(value, round) - Math.min(actual.radius[index], round)) > limits.radius);
      if (differs) report(node, actual, 'radius', `radius ${[...new Set(design)].join('/')} → ${[...new Set(actual.radius)].join('/')}`, 25);
    }
    const fill = node.fills?.length === 1 && node.fills[0].color ? node.fills[0] : null;
    if (fill && actual.background && fill.opacity >= 1 && actual.background.a >= 1 && colourGap(fill.color, actual.background) > limits.colour) {
      report(node, actual, 'colour', `background ${fill.color} → ${hexOf(actual.background)}`, 35);
    }
    if (node.stroke && typeof node.stroke.weight === 'number' && actual.border && Math.abs(actual.border.width - node.stroke.weight) > 0.5) {
      report(node, actual, 'border', `border ${node.stroke.weight} → ${actual.border.width}`, 20);
    }
  }

  // 5. A subtree of which nothing is on the page is one finding, named by its top.
  const absent = new Set(issues.filter((issue) => issue.kind === 'missing').map((issue) => issue.id));
  const gone = (node) => !found.has(node.id) && inside(node).every((child) => !found.has(child.id));
  for (const node of nodes) {
    if (!gone(node) || (byId.get(node.parent) && !hidden(byId.get(node.parent)) && gone(byId.get(node.parent)))) continue;
    const lost = [node, ...inside(node)].filter((child) => absent.has(child.id));
    if (lost.length < 2) continue;
    const first = issues.find((issue) => issue.id === lost[0].id);
    for (const child of lost.slice(1)) issues.splice(issues.findIndex((issue) => issue.id === child.id), 1);
    Object.assign(first, { id: node.id, ref: node.ref, layer: node.name, text: `nothing of it is on the page (${lost.length} layers)`, weight: 90 + Math.min(lost.length, 9),
      box: [node.box[0] + (carried(node) || [0, 0])[0], node.box[1] + (carried(node) || [0, 0])[1], node.box[2], node.box[3]] });
  }
  // The same difference on many layers has one cause: it is said once, with how many it touches.
  const same = new Map();
  for (const issue of issues.filter((value) => value.kind !== 'missing' && value.kind !== 'position')) {
    const key = `${issue.kind}|${issue.text}`;
    if (!same.has(key)) same.set(key, []);
    same.get(key).push(issue);
  }
  for (const group of same.values()) {
    if (group.length < 3) continue;
    for (const issue of group.slice(1)) issues.splice(issues.indexOf(issue), 1);
    group[0].layer = `${group.length} layers, the first ${group[0].layer}`;
    group[0].weight += Math.min(group.length, 20);
  }
  issues.sort((a, b) => b.weight - a.weight);
  const matchedWords = new Set([...usedText].map((index) => page.texts[index].content));
  const extra = page.texts.filter((text, index) => text.kind === 'run' && !usedText.has(index) && !matchedWords.has(text.content));
  return { issues, compared: found.size, total: spec.nodes.length, skipped, extra: extra.length };
}

export function summary(result, pixels, pictures, limit = 20) {
  const kinds = {};
  for (const issue of result.issues) kinds[issue.kind] = (kinds[issue.kind] || 0) + 1;
  const counts = Object.entries(kinds).map(([kind, count]) => `${count} ${kind}`).join(', ');
  const lines = [`design check: ${result.issues.length ? 'FAIL' : 'PASS'} — ${result.issues.length ? `${result.issues.length} ${result.known === undefined ? '' : 'new '}differences (${counts})` : result.known === undefined ? 'no differences' : 'no new differences'}` +
    ` · compared ${result.compared} of ${result.total} layers` + (pixels === undefined ? '' : ` · ${(pixels * 100).toFixed(1)}% of pixels differ`)];
  result.issues.slice(0, limit).forEach((issue, index) => {
    lines.push(`${String(index + 1).padStart(2)} ‹${issue.ref}› ${issue.layer}${issue.element ? ` → ${issue.element}` : ''}: ${issue.text}`);
  });
  if (result.issues.length > limit) lines.push(`   … and ${result.issues.length - limit} more in report.json`);
  const notes = [];
  if (result.skipped.unpainted) notes.push(`${result.skipped.unpainted} layers paint nothing and are judged through their children`);
  if (result.skipped.ignored) notes.push(`${result.skipped.ignored} ignored by the map`);
  if (result.extra) notes.push(`${result.extra} texts on the page are not in the design`);
  if (result.known) notes.push(`${result.known} differences were there before the task and are not listed`);
  if (notes.length) lines.push(`not compared: ${notes.join('; ')}`);
  if (pictures) lines.push(`pictures: ${pictures.marked} (numbered as above), ${pictures.diff}`);
  return lines.join('\n') + '\n';
}
