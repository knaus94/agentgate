// A design as a worker reads it. Whatever a worker reads stays in its context for the rest of its session, so
// the brief carries intent — layout rules, type, colour, tokens — and leaves everything else to be asked for:
// a component instance is one line, a repeated sibling is one item of a list, and positions that follow from
// auto layout are not printed. `detail` prints one node in full, on request.
import crypto from 'node:crypto';

const round = (value) => Math.round(value * 100) / 100;
const hex = ({ r, g, b }) => '#' + [r, g, b].map((channel) => Math.round(channel * 255).toString(16).padStart(2, '0')).join('').toUpperCase();

const shapes = new Set(['VECTOR', 'BOOLEAN_OPERATION', 'STAR', 'LINE', 'REGULAR_POLYGON', 'ELLIPSE', 'RECTANGLE']);
const drawn = new Set(['VECTOR', 'BOOLEAN_OPERATION', 'STAR', 'LINE', 'REGULAR_POLYGON']);
const iconLimit = 128;

const visible = (node) => node.visible !== false;
const hasImage = (node) => (node.fills || []).some((fill) => fill.type === 'IMAGE' && fill.visible !== false);
const paints = (node) => [...(node.fills || []), ...(node.strokes || [])].some((value) => value.visible !== false);

// A subtree made only of vector shapes is one picture: it is exported whole and not described part by part.
// A frame that paints a background of its own is a box with a picture inside, so it is described as a box.
function isDrawing(node) {
  if (!visible(node)) return true;
  if (node.type === 'TEXT' || hasImage(node)) return false;
  if (shapes.has(node.type)) return true;
  if (node.type !== 'GROUP' && paints(node)) return false;
  return (node.children || []).length > 0 && node.children.every(isDrawing);
}

function hasDrawnPart(node) {
  return visible(node) && (drawn.has(node.type) || (node.children || []).some(hasDrawnPart));
}

function paint(fill, token) {
  if (fill.visible === false) return null;
  const opacity = round((fill.opacity ?? 1) * (fill.color?.a ?? 1));
  if (fill.type === 'SOLID') return { color: hex(fill.color), opacity, token };
  if (fill.type.startsWith('GRADIENT')) {
    return { gradient: fill.type.replace('GRADIENT_', '').toLowerCase(), opacity: round(fill.opacity ?? 1),
      stops: (fill.gradientStops || []).map((stop) => ({ at: round(stop.position), color: hex(stop.color), opacity: round(stop.color.a ?? 1) })) };
  }
  if (fill.type === 'IMAGE') return { image: true, fit: (fill.scaleMode || '').toLowerCase() };
  return { kind: fill.type.toLowerCase() };
}

function effect(value) {
  if (value.visible === false) return null;
  if (value.type === 'DROP_SHADOW' || value.type === 'INNER_SHADOW') {
    return { shadow: value.type === 'INNER_SHADOW' ? 'inner' : 'drop', x: round(value.offset?.x ?? 0), y: round(value.offset?.y ?? 0),
      blur: round(value.radius ?? 0), spread: round(value.spread ?? 0), color: hex(value.color), opacity: round(value.color.a ?? 1) };
  }
  if (value.type === 'LAYER_BLUR') return { blur: round(value.radius ?? 0) };
  if (value.type === 'BACKGROUND_BLUR') return { backdropBlur: round(value.radius ?? 0) };
  return { kind: value.type.toLowerCase() };
}

const align = { MIN: 'start', CENTER: 'center', MAX: 'end', SPACE_BETWEEN: 'space-between', BASELINE: 'baseline' };

function textRuns(node) {
  const table = node.styleOverrideTable || {};
  const marks = node.characterStyleOverrides || [];
  if (Object.keys(table).length === 0 || marks.length === 0) return undefined;
  const runs = [];
  [...node.characters].forEach((character, index) => {
    const key = marks[index] || 0;
    const last = runs[runs.length - 1];
    if (last && last.key === key) last.text += character;
    else runs.push({ key, text: character });
  });
  if (runs.length < 2) return undefined;
  return runs.map(({ key, text }) => {
    const override = table[key] || {};
    const fill = (override.fills || []).map((value) => paint(value)).find(Boolean);
    return { text, weight: override.fontWeight, size: override.fontSize, color: fill?.color, opacity: fill?.opacity };
  });
}

// The whole of a node, normalised. This is what the check compares with and what `detail` prints.
export function buildSpec(rest, variables = {}) {
  const root = rest.document;
  const origin = root.absoluteBoundingBox;
  const tokenName = (alias) => (alias && variables[alias.id] ? variables[alias.id].name : undefined);
  const nodes = [];
  const refs = new Set();

  const walk = (node, depth, parent) => {
    if (!visible(node)) return;
    const box = node.absoluteBoundingBox;
    // A short handle a worker can quote back: the id of a layer inside an instance runs to sixty characters.
    const digest = crypto.createHash('sha256').update(node.id).digest('hex');
    let ref = digest.slice(0, 4);
    for (let length = 5; refs.has(ref); length++) ref = digest.slice(0, length);
    refs.add(ref);
    const out = { ref, id: node.id, name: node.name, type: node.type, depth, parent };
    if (box) out.box = [round(box.x - origin.x), round(box.y - origin.y), round(box.width), round(box.height)];
    const bound = node.boundVariables || {};

    if (node.type === 'INSTANCE' && rest.components?.[node.componentId]) {
      const component = rest.components[node.componentId];
      const set = component.componentSetId && rest.componentSets?.[component.componentSetId];
      out.component = set ? { name: set.name, variant: component.name } : { name: component.name };
    }
    if (node.layoutMode && node.layoutMode !== 'NONE') {
      out.layout = { mode: { HORIZONTAL: 'row', VERTICAL: 'column', GRID: 'grid' }[node.layoutMode] || node.layoutMode.toLowerCase(),
        gap: round(node.itemSpacing ?? 0),
        padding: [node.paddingTop, node.paddingRight, node.paddingBottom, node.paddingLeft].map((value) => round(value ?? 0)),
        justify: align[node.primaryAxisAlignItems || 'MIN'], align: align[node.counterAxisAlignItems || 'MIN'] };
      if (node.layoutWrap === 'WRAP') out.layout.wrap = round(node.counterAxisSpacing ?? 0);
      if (node.layoutMode === 'GRID') out.layout.grid = { columns: node.gridColumnCount, rows: node.gridRowCount, columnGap: node.gridColumnGap, rowGap: node.gridRowGap };
      const tokens = { gap: tokenName(bound.itemSpacing), padding: tokenName(bound.paddingLeft) || tokenName(bound.paddingTop) };
      if (tokens.gap || tokens.padding) out.layout.tokens = tokens;
    }
    if (node.layoutSizingHorizontal || node.layoutSizingVertical) {
      out.sizing = [(node.layoutSizingHorizontal || 'FIXED').toLowerCase(), (node.layoutSizingVertical || 'FIXED').toLowerCase()];
    }
    if (node.layoutPositioning === 'ABSOLUTE') out.absolute = true;
    if (node.rectangleCornerRadii) out.radius = node.rectangleCornerRadii.map(round);
    else if (node.cornerRadius) out.radius = round(node.cornerRadius);
    const radiusToken = tokenName(bound.rectangleCornerRadii?.RECTANGLE_TOP_LEFT_CORNER_RADIUS) || tokenName(bound.cornerRadius);
    if (radiusToken) out.radiusToken = radiusToken;
    const fills = (node.fills || []).map((fill, index) => paint(fill, tokenName(bound.fills?.[index]))).filter(Boolean);
    if (fills.length) out.fills = fills;
    const strokes = (node.strokes || []).map((stroke, index) => paint(stroke, tokenName(bound.strokes?.[index]))).filter(Boolean);
    if (strokes.length && (node.strokeWeight || node.individualStrokeWeights)) {
      out.stroke = { weight: node.individualStrokeWeights ? ['top', 'right', 'bottom', 'left'].map((side) => round(node.individualStrokeWeights[side] ?? 0)) : round(node.strokeWeight),
        align: (node.strokeAlign || 'INSIDE').toLowerCase(), paints: strokes };
    }
    const effects = (node.effects || []).map(effect).filter(Boolean);
    if (effects.length) out.effects = effects;
    if (node.opacity !== undefined && node.opacity < 1) out.opacity = round(node.opacity);
    if (node.clipsContent) out.clips = true;

    if (node.type === 'TEXT') {
      const style = node.style || {};
      out.text = { content: node.characters, family: style.fontFamily, weight: style.fontWeight, size: round(style.fontSize ?? 0),
        lineHeight: style.lineHeightPx === undefined ? undefined : round(style.lineHeightPx), letterSpacing: round(style.letterSpacing ?? 0),
        align: (style.textAlignHorizontal || 'LEFT').toLowerCase(), case: style.textCase && style.textCase !== 'ORIGINAL' ? style.textCase.toLowerCase() : undefined,
        italic: style.italic || undefined, style: rest.styles?.[node.styles?.text]?.name, runs: textRuns(node) };
    }

    const drawing = node.type !== 'TEXT' && box && isDrawing(node) && hasDrawnPart(node);
    const picture = hasImage(node) && (node.children || []).every((child) => !visible(child) || isDrawing(child));
    nodes.push(out);
    if (drawing || picture) {
      out.asset = { format: drawing ? 'svg' : 'png', kind: drawing && Math.max(box.width, box.height) <= iconLimit ? 'icon' : drawing ? 'graphic' : 'image' };
      return;
    }
    for (const child of node.children || []) walk(child, depth + 1, node.id);
  };
  walk(root, 0, null);
  return { frame: { id: root.id, name: root.name, width: round(origin.width), height: round(origin.height) }, nodes };
}

// The same file name for the same node on every pull: the layer name for a person, a digest of the id for uniqueness.
export function assetName(node) {
  const slug = node.name.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-+|-+$/g, '').slice(0, 32) || node.asset.kind;
  return `${slug}-${crypto.createHash('sha256').update(node.id).digest('hex').slice(0, 8)}.${node.asset.format}`;
}

const percent = (value) => (value === undefined || value >= 1 ? '' : ` ${Math.round(value * 100)}%`);
const quote = (text, limit = 70) => JSON.stringify(text.length > limit ? text.slice(0, limit) + '…' : text);

function paintText(value) {
  if (value.color) return `${value.color}${percent(value.opacity)}${value.token ? ` (${value.token})` : ''}`;
  if (value.gradient) return `${value.gradient}-gradient(${value.stops.map((stop) => `${stop.color}${percent(stop.opacity)} ${Math.round(stop.at * 100)}%`).join(', ')})${percent(value.opacity)}`;
  if (value.image) return `image (${value.fit})`;
  return value.kind;
}

function padding(values) {
  const [top, right, bottom, left] = values;
  if (top === right && right === bottom && bottom === left) return String(top);
  return top === bottom && right === left ? `${top}/${right}` : values.join('/');
}

function fontText(text, full) {
  const numbers = `${text.family} ${text.weight} ${text.size}/${text.lineHeight ?? 'auto'}${text.letterSpacing ? ` ls ${text.letterSpacing}` : ''}`;
  let font = text.style ? (full ? `${numbers} “${text.style}”` : `“${text.style}”`) : numbers;
  if (text.align !== 'left') font += ` ${text.align}`;
  if (text.case) font += ` ${text.case}`;
  return font;
}

function boxParts(node, full) {
  const parts = [];
  if (node.layout) {
    const layout = node.layout;
    let text = `${layout.mode} gap ${layout.gap}${layout.tokens?.gap ? ` (${layout.tokens.gap})` : ''}`;
    if (layout.padding.some(Boolean)) text += ` pad ${padding(layout.padding)}${layout.tokens?.padding ? ` (${layout.tokens.padding})` : ''}`;
    if (layout.justify !== 'start') text += ` justify ${layout.justify}`;
    if (layout.align !== 'start') text += ` align ${layout.align}`;
    if (layout.wrap !== undefined) text += ` wrap ${layout.wrap}`;
    if (layout.grid) text += ` ${layout.grid.columns}×${layout.grid.rows} gaps ${layout.grid.columnGap}/${layout.grid.rowGap}`;
    parts.push(text);
  }
  if (node.text) {
    parts.push(fontText(node.text, full));
    if (node.text.runs) parts.push('runs ' + node.text.runs.map((run) => `${quote(run.text, 30)}${run.color ? ' ' + run.color : ''}${run.weight ? ' ' + run.weight : ''}`).join(' + '));
  }
  if (node.fills && !node.asset) parts.push(`${node.text ? 'color' : 'bg'} ${node.fills.map(paintText).join(' + ')}`);
  if (node.stroke) parts.push(`stroke ${[].concat(node.stroke.weight).join('/')} ${node.stroke.align} ${node.stroke.paints.map(paintText).join(' + ')}`);
  if (node.radius !== undefined) parts.push(`r ${[].concat(node.radius).join('/')}${node.radiusToken ? ` (${node.radiusToken})` : ''}`);
  for (const value of node.effects || []) {
    if (value.shadow) parts.push(`${value.shadow}-shadow ${value.x} ${value.y} ${value.blur} ${value.spread} ${value.color}${percent(value.opacity)}`);
    else if (value.blur !== undefined) parts.push(`blur ${value.blur}`);
    else if (value.backdropBlur !== undefined) parts.push(`backdrop-blur ${value.backdropBlur}`);
  }
  if (node.opacity !== undefined) parts.push(`opacity ${node.opacity}`);
  if (node.clips) parts.push('clips');
  return parts;
}

const componentText = (node) => (node.component ? ` ⟨${node.component.name}${node.component.variant ? `: ${node.component.variant}` : ''}⟩` : '');
const generic = /^(Frame|Group|Rectangle|Vector|Ellipse|Line|Text) ?\d*$/;

function tree(spec) {
  const byId = new Map(spec.nodes.map((node) => [node.id, { ...node, children: [] }]));
  for (const node of byId.values()) byId.get(node.parent)?.children.push(node);
  return byId;
}

// Everything about one node and what is under it: the answer to a worker that needs more than the brief says.
export function detail(spec, wanted, depth = 3) {
  const nodes = tree(spec);
  const start = [...nodes.values()].find((node) => node.ref === wanted || node.id === wanted || node.name === wanted);
  if (!start) return null;
  const out = [];
  const print = (node, level) => {
    const [x, y, width, height] = node.box || [];
    const label = node.text ? quote(node.text.content, 200) : node.name;
    const parts = [`${width}×${height} @${x},${y}`];
    if (node.sizing) parts.push(`w:${node.sizing[0]} h:${node.sizing[1]}`);
    if (node.absolute) parts.push('absolute');
    if (node.asset) parts.push(`${node.asset.kind} ${node.asset.file || '(not exported)'}`);
    out.push(`${'  '.repeat(level)}${label}${componentText(node)} ${node.type.toLowerCase()} · ${[...parts, ...boxParts(node, true)].join(' · ')}  ‹${node.ref}›`);
    if (level + 1 > depth && node.children.length) out.push(`${'  '.repeat(level + 1)}… ${node.children.length} more inside: ask for ‹${node.ref}› children by their own refs`);
    else for (const child of node.children) print(child, level + 1);
  };
  print(start, 0);
  return out.join('\n') + '\n';
}

// What is inside a subtree, in one phrase: its texts and its pictures.
function contents(node) {
  const out = [];
  const visit = (current) => {
    if (current.text) out.push(quote(current.text.content, 40));
    else if (current.asset) out.push(current.asset.file ? current.asset.file.replace(/^assets\//, '') : current.name);
    for (const child of current.children) visit(child);
  };
  for (const child of node.children) visit(child);
  if (node.asset && node.asset.file) out.push(node.asset.file.replace(/^assets\//, ''));
  return [...new Set(out)];
}

export function brief(spec, source, dir) {
  const nodes = tree(spec);
  const root = nodes.get(spec.frame.id);
  const styles = new Map();
  const note = (node) => {
    if (node.text?.style) styles.set(node.text.style, node.text);
  };

  const lineOf = (node, parent) => {
    const [x, y, width, height] = node.box || [];
    const parts = [];
    const flowing = parent?.layout && !node.absolute;
    const [horizontal, vertical] = node.sizing || ['fixed', 'fixed'];
    if (!flowing) {
      parts.push(`${width}×${height}${parent ? ` @${round(x - parent.box[0])},${round(y - parent.box[1])}` : ''}${node.absolute ? ' absolute' : ''}`);
    } else if (horizontal === 'fixed' && vertical === 'fixed') {
      parts.push(`${width}×${height}`);
    } else {
      // "hug" is what a box does by itself; only what differs from it is worth a word.
      if (horizontal !== 'hug') parts.push(`w:${horizontal === 'fixed' ? width : horizontal}`);
      if (vertical !== 'hug') parts.push(`h:${vertical === 'fixed' ? height : vertical}`);
    }
    if (node.asset) return { head: generic.test(node.name) ? node.asset.kind : `${node.name} ${node.asset.kind}`, parts: [...parts, node.asset.file || '(not exported)'] };
    if (node.component) {
      // The inside of a component belongs to the component: here it is only what this use of it shows.
      const inside = contents(node);
      const name = node.name === node.component.name ? '' : node.name;
      return { head: `${name}${componentText(node)}`.trim(), parts: inside.length ? [...parts, inside.join(', ')] : parts };
    }
    note(node);
    const label = node.text ? quote(node.text.content, 300) : generic.test(node.name) ? node.type.toLowerCase() : node.name;
    return { head: label, parts: [...parts, ...boxParts(node)] };
  };

  // Two siblings are the same when they differ only in their words and pictures.
  const shape = (node, parent) => {
    const { head, parts } = lineOf(node, parent);
    const own = `${node.text ? 'T' : node.asset ? 'A' : head}|${parts.join('|')}`.replace(/"(?:[^"\\]|\\.)*"/g, '""').replace(/[\w-]+\.(svg|png)/g, '*');
    return node.component || node.asset ? own : `${own}{${node.children.map((child) => shape(child, node)).join(',')}}`;
  };

  const out = [];
  const print = (node, parent, level) => {
    const { head, parts } = lineOf(node, parent);
    out.push(`${'  '.repeat(level)}${head}${parts.length ? ' · ' + parts.join(' · ') : ''}  ‹${node.ref}›`);
    if (node.component || node.asset) return;
    // The first of every kind of child is described; the rest of its kind are listed by what they show.
    const kinds = new Map();
    node.children.forEach((child, index) => {
      const key = shape(child, node);
      if (!kinds.has(key)) kinds.set(key, []);
      kinds.get(key).push({ child, index });
    });
    for (const members of kinds.values()) {
      const repeated = members.length >= 3;
      for (const { child } of repeated ? members.slice(0, 1) : members) print(child, node, level + 1);
      if (!repeated) continue;
      const contiguous = members.every((member, position) => member.index === members[0].index + position);
      const others = members.slice(1).map(({ child, index }) => {
        const shows = contents({ ...child, children: child.children.length ? child.children : [child] }).join(' ') || child.name;
        return `${contiguous ? '' : `#${index + 1} `}${shows} ‹${child.ref}›`;
      });
      out.push(`${'  '.repeat(level + 1)}… and ${others.length} more built the same${contiguous ? '' : ' (# is the place among the children)'}: ${others.join(' | ')}`);
    }
  };
  print(root, null, 0);

  const head = [`# ${spec.frame.name} — ${spec.frame.width}×${spec.frame.height}`, '',
    `${source}. Picture: \`frame.png\`. Icons and pictures: \`assets/\`.`, '',
    'One line per layer. Sizes are CSS pixels; `fill` and `hug` are auto-layout sizing; a child of a row, column or grid has no',
    'position because the layout gives it one. A name in parentheses is the design token bound to the value. `⟨…⟩` marks a',
    'component instance: use the project\'s component, the line lists only what this use shows. A handle like `‹a3f1›` names a layer:',
    `\`figma node ${dir} a3f1\` prints everything about it and its inside. Do not read the files under \`data/\`: they are for tools.`, ''];
  const tail = [];
  if (styles.size) {
    tail.push('', 'Text styles:');
    for (const [name, text] of [...styles].sort()) tail.push(`- ${name}: ${text.family} ${text.weight} ${text.size}/${text.lineHeight ?? 'auto'}${text.letterSpacing ? ` ls ${text.letterSpacing}` : ''}`);
  }
  return [...head, ...out, ...tail].join('\n') + '\n';
}
