// What is read from the implementation: every visible text and every element that paints something, with its
// box and computed style. Runs inside the page. Coordinates are relative to the root element.

export function readPage(input) {
  const rootSelector = typeof input === 'string' ? input : input?.root || '';
  const select = typeof input === 'object' && input ? input.select || {} : {};
  const root = rootSelector ? document.querySelector(rootSelector) : document.documentElement;
  if (!root) return { error: `no element matches ${rootSelector}`, missingRoot: true };
  const selected = new Map();
  const unresolved = [];
  for (const [ref, selector] of Object.entries(select)) {
    let matches;
    try { matches = [...(root.matches(selector) ? [root] : []), ...root.querySelectorAll(selector)]; }
    catch { return { error: `map.select ${ref} has an invalid CSS selector: ${selector}` }; }
    if (matches.length > 1) return { error: `map.select ${ref} matches more than one element: ${selector}` };
    if (matches.length === 0) { unresolved.push([ref, selector]); continue; }
    if (!selected.has(matches[0])) selected.set(matches[0], []);
    selected.get(matches[0]).push(ref);
  }
  const origin = rootSelector ? root.getBoundingClientRect() : { left: -window.scrollX, top: -window.scrollY };
  const round = (value) => Math.round(value * 100) / 100;
  const box = (rect) => [round(rect.left - origin.left), round(rect.top - origin.top), round(rect.width), round(rect.height)];
  const colours = new Map();
  let colourCanvas;
  const rgba = (value) => {
    if (!value) return null;
    if (colours.has(value)) return colours.get(value);
    let colour = null;
    const match = /^rgba?\(([^)]+)\)$/.exec(value);
    if (match) {
      const parts = match[1].split(/[\s,/]+/).filter(Boolean);
      const numbers = parts.map((part, index) => part.endsWith('%')
        ? parseFloat(part) * (index === 3 ? 0.01 : 2.55) : Number(part));
      if ((numbers.length === 3 || numbers.length === 4) && numbers.every(Number.isFinite)) {
        const [r, g, b, a = 1] = numbers;
        colour = { r, g, b, a };
      }
    }
    // Modern computed styles can retain oklch(), lab() or color(). Let the same
    // browser convert them to the screenshot's sRGB space instead of guessing.
    if (!colour && CSS.supports('color', value)) {
      colourCanvas ||= Object.assign(document.createElement('canvas'), { width: 1, height: 1 }).getContext('2d', { willReadFrequently: true });
      if (colourCanvas) {
        colourCanvas.clearRect(0, 0, 1, 1);
        colourCanvas.fillStyle = value;
        colourCanvas.fillRect(0, 0, 1, 1);
        const [r, g, b, a] = colourCanvas.getImageData(0, 0, 1, 1).data;
        colour = { r, g, b, a: a / 255 };
      }
    }
    colours.set(value, colour);
    return colour;
  };
  const px = (value) => (value.endsWith('px') ? round(parseFloat(value)) : undefined);
  const describe = (element) => {
    let text = element.tagName.toLowerCase();
    if (element.id) text += '#' + element.id;
    const names = [...element.classList].slice(0, 2).map((name) => name.replace(/^_+|_[A-Za-z0-9]{5,}$/g, ''));
    if (names.length) text += '.' + names.join('.');
    return text;
  };
  // An element shows only as much as its ancestors let through.
  const shown = (element) => {
    for (let current = element; current && current.nodeType === 1; current = current.parentElement) {
      const style = getComputedStyle(current);
      if (style.display === 'none' || style.visibility === 'hidden' || parseFloat(style.opacity) === 0) return false;
    }
    return true;
  };

  // A family the browser does not have is drawn with the fallback, so it measures exactly as the fallback does.
  const ruler = document.createElement('canvas').getContext('2d');
  const known = new Map();
  const generic = new Set(['serif', 'sans-serif', 'monospace', 'cursive', 'fantasy', 'system-ui', 'ui-sans-serif', 'ui-serif', 'ui-monospace']);
  const measure = (font) => {
    ruler.font = `72px ${font}`;
    return ruler.measureText('mmwwii10 Жщ').width;
  };
  const available = (name) => {
    if (generic.has(name)) return true;
    if (!known.has(name)) known.set(name, ['monospace', 'serif'].some((fallback) => measure(`"${name}", ${fallback}`) !== measure(fallback)));
    return known.get(name);
  };

  const texts = [];
  const seen = new Set();
  const addText = (element, range, content, kind) => {
    const rect = range.getBoundingClientRect();
    const clean = content.replace(/\s+/g, ' ').trim();
    if (!clean || rect.width < 1 || rect.height < 1) return;
    const key = `${kind}|${clean}|${Math.round(rect.left)}|${Math.round(rect.top)}`;
    if (seen.has(key)) return;
    seen.add(key);
    const style = getComputedStyle(element);
    const families = style.fontFamily.split(',').map((name) => name.trim().replace(/^["']|["']$/g, ''));
    // The family the browser really drew with: the first one in the list that it has.
    const loaded = families.find(available);
    texts.push({ kind, content: clean, box: box(rect), where: describe(element), family: families[0], rendered: loaded || families[families.length - 1],
      size: px(style.fontSize), weight: Number(style.fontWeight) || style.fontWeight, lineHeight: px(style.lineHeight),
      letterSpacing: style.letterSpacing === 'normal' ? 0 : px(style.letterSpacing), color: rgba(style.color),
      transform: style.textTransform === 'none' ? undefined : style.textTransform, align: style.textAlign });
  };
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  const blocks = new Set();
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const element = node.parentElement;
    if (!element || !node.nodeValue.trim() || !shown(element) || element.closest('script,style,noscript,template')) continue;
    const range = document.createRange();
    range.selectNodeContents(node);
    addText(element, range, node.nodeValue, 'run');
    // A design text with two colours is one layer and several elements: the enclosing line is read as well.
    let block = element;
    while (block.parentElement && block !== root && getComputedStyle(block).display.startsWith('inline')) block = block.parentElement;
    if (block !== element) blocks.add(block);
  }
  for (const block of blocks) {
    if ([...block.children].some((child) => !getComputedStyle(child).display.startsWith('inline'))) continue;
    const range = document.createRange();
    range.selectNodeContents(block);
    addText(block, range, block.innerText || block.textContent, 'block');
  }

  const boxes = [];
  const media = new Set(['IMG', 'SVG', 'CANVAS', 'VIDEO', 'INPUT', 'TEXTAREA', 'SELECT', 'BUTTON', 'PICTURE']);
  // The root is a layer of the design like any other.
  for (const element of [root, ...root.querySelectorAll('*')]) {
    if (element.closest('svg') && element.tagName.toLowerCase() !== 'svg') continue;
    const rect = element.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1 || !shown(element)) continue;
    const style = getComputedStyle(element);
    const background = rgba(style.backgroundColor);
    const border = px(style.borderTopWidth) || 0;
    const painted = (background && background.a > 0) || style.backgroundImage !== 'none' || border > 0 || style.boxShadow !== 'none' ||
      media.has(element.tagName.toUpperCase());
    const layout = style.display.includes('flex') || style.display.includes('grid');
    if (!painted && !layout && !selected.has(element) && element !== root) continue;
    boxes.push({ box: box(rect), where: describe(element), painted, tag: element.tagName.toLowerCase(),
      root: element === root,
      refs: selected.get(element),
      background: background && background.a > 0 ? background : undefined, image: style.backgroundImage !== 'none' || undefined,
      border: border > 0 ? { width: border, color: rgba(style.borderTopColor) } : undefined,
      radius: [style.borderTopLeftRadius, style.borderTopRightRadius, style.borderBottomRightRadius, style.borderBottomLeftRadius].map((value) => px(value) ?? value),
      padding: [style.paddingTop, style.paddingRight, style.paddingBottom, style.paddingLeft].map((value) => px(value) ?? 0),
      gap: layout ? [px(style.rowGap) ?? 0, px(style.columnGap) ?? 0] : undefined,
      direction: layout ? (style.display.includes('grid') ? 'grid' : style.flexDirection.startsWith('column') ? 'column' : 'row') : undefined,
      opacity: parseFloat(style.opacity) < 1 ? parseFloat(style.opacity) : undefined });
  }
  // Earlier maps used the compact labels from the report rather than real CSS selectors.
  for (const [ref, selector] of unresolved) {
    const matches = boxes.filter((box) => box.where === selector);
    if (matches.length > 1) return { error: `map.select ${ref} matches more than one report label: ${selector}` };
    if (matches.length === 1) (matches[0].refs ??= []).push(ref);
  }
  const rootRect = root.getBoundingClientRect();
  const rootStyle = getComputedStyle(root);
  return { texts, boxes, documentWidth: document.documentElement.clientWidth,
    horizontalOverflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
    root: { x: round(rootRect.left + window.scrollX), y: round(rootRect.top + window.scrollY),
      width: round(rootRect.width), height: round(rootRect.height),
      padding: [rootStyle.paddingTop, rootStyle.paddingRight, rootStyle.paddingBottom, rootStyle.paddingLeft].map((value) => px(value) ?? 0) } };
}

// Motion makes two runs differ: it is stopped before anything is measured.
export const still = `*, *::before, *::after { animation: none !important; transition: none !important; caret-color: transparent !important; scroll-behavior: auto !important; }`;

// Compare at native size, retaining areas present in only one picture.
export async function diffImages({ design, actual, crop, marks }) {
  const load = (source) => new Promise((resolve, reject) => {
    const image = new Image();
    image.onload = () => resolve(image);
    image.onerror = () => reject(new Error('a picture could not be decoded'));
    image.src = source;
  });
  const [left, right] = await Promise.all([load(design), load(actual)]);
  const width = Math.ceil(Math.max(crop.width, right.width));
  const height = Math.ceil(Math.max(crop.height, right.height));
  const canvas = (image, x, y, clipWidth = width, clipHeight = height) => {
    const element = document.createElement('canvas');
    element.width = width;
    element.height = height;
    const context = element.getContext('2d', { willReadFrequently: true });
    context.beginPath(); context.rect(0, 0, clipWidth, clipHeight); context.clip();
    context.drawImage(image, -x, -y);
    return { element, context, data: context.getImageData(0, 0, width, height) };
  };
  const a = canvas(left, crop.x, crop.y, crop.width, crop.height);
  const b = canvas(right, 0, 0);
  const out = a.context.createImageData(width, height);
  let different = 0;
  for (let index = 0; index < a.data.data.length; index += 4) {
    const dr = a.data.data[index] - b.data.data[index];
    const dg = a.data.data[index + 1] - b.data.data[index + 1];
    const db = a.data.data[index + 2] - b.data.data[index + 2];
    // Perceived difference; small ones are antialiasing, not layout.
    const delta = 0.299 * dr * dr + 0.587 * dg * dg + 0.114 * db * db;
    const grey = 0.1 * (0.299 * b.data.data[index] + 0.587 * b.data.data[index + 1] + 0.114 * b.data.data[index + 2]) + 20;
    const x = index / 4 % width;
    const y = Math.floor(index / 4 / width);
    const missing = (x < crop.width && y < crop.height) !== (x < right.width && y < right.height);
    if (missing || delta > 900) {
      different++;
      out.data.set([255, 40, 40, 255], index);
    } else {
      out.data.set([grey, grey, grey, 255], index);
    }
  }
  a.context.putImageData(out, 0, 0);
  const diff = a.element.toDataURL('image/png');
  // The implementation with every reported place framed and numbered.
  const pen = b.context;
  pen.lineWidth = 2;
  pen.font = 'bold 12px sans-serif';
  for (const mark of marks) {
    pen.strokeStyle = mark.color;
    pen.strokeRect(mark.box[0], mark.box[1], mark.box[2], mark.box[3]);
    const label = String(mark.number);
    const labelWidth = pen.measureText(label).width + 6;
    pen.fillStyle = mark.color;
    pen.fillRect(mark.box[0], Math.max(0, mark.box[1] - 15), labelWidth, 15);
    pen.fillStyle = '#000';
    pen.fillText(label, mark.box[0] + 3, Math.max(11, mark.box[1] - 4));
  }
  return { ratio: different / (width * height), width, height, diff, marked: b.element.toDataURL('image/png'), designCrop: (() => {
    const copy = canvas(left, crop.x, crop.y, crop.width, crop.height);
    return copy.element.toDataURL('image/png');
  })() };
}
