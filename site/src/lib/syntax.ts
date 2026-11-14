import { codeToHtml, type DecorationItem, type ThemeRegistration } from 'shiki';

export type Lang = 'bash' | 'json' | 'toml' | 'text' | 'html' | 'md' | 'diff';

const v = (name: string) => `var(--syn-${name})`;

// One theme whose colors are CSS variables, so light, dark and the always-dark agent session all come from
// global.css instead of two fixed editor themes.
export const theme: ThemeRegistration = {
  name: 'huntsman',
  type: 'light',
  colors: { 'editor.foreground': v('fg'), 'editor.background': 'transparent' },
  tokenColors: [
    { settings: { foreground: v('fg') } },
    { scope: ['comment', 'punctuation.definition.comment'], settings: { foreground: v('comment'), fontStyle: 'italic' } },
    { scope: ['punctuation', 'meta.brace', 'keyword.operator'], settings: { foreground: v('punct') } },
    { scope: ['keyword', 'storage', 'keyword.control'], settings: { foreground: v('keyword') } },
    { scope: ['string', 'string.quoted', 'string.template'], settings: { foreground: v('string') } },
    { scope: ['string.unquoted.argument.shell'], settings: { foreground: v('fg') } },
    { scope: ['constant.numeric', 'constant.language', 'constant.character'], settings: { foreground: v('number') } },
    { scope: ['constant.character.escape'], settings: { foreground: v('punct') } },
    {
      scope: ['entity.name.function', 'support.function', 'entity.name.command'],
      settings: { foreground: v('function') },
    },
    { scope: ['constant.other.option', 'variable.parameter', 'variable.other'], settings: { foreground: v('param') } },
    { scope: ['entity.name.tag', 'punctuation.definition.tag'], settings: { foreground: v('tag') } },
    { scope: ['entity.other.attribute-name'], settings: { foreground: v('attr') } },
    { scope: ['support.type.property-name', 'meta.object-literal.key'], settings: { foreground: v('property') } },
    {
      scope: ['markup.heading', 'entity.name.section', 'punctuation.definition.heading'],
      settings: { foreground: v('heading'), fontStyle: 'bold' },
    },
    { scope: ['string.other.link.title', 'string.other.link.description'], settings: { foreground: v('fg') } },
    { scope: ['markup.underline.link'], settings: { foreground: v('link') } },
    { scope: ['markup.bold'], settings: { fontStyle: 'bold' } },
    { scope: ['markup.italic'], settings: { fontStyle: 'italic' } },
    { scope: ['markup.inserted', 'punctuation.definition.inserted'], settings: { foreground: v('inserted') } },
    { scope: ['markup.deleted', 'punctuation.definition.deleted'], settings: { foreground: v('deleted') } },
  ],
};

/** Highlight code at build time. Decorations add classes to character ranges, e.g. to strike out a tracker URL. */
export const highlight = (code: string, lang: Lang, decorations: DecorationItem[] = []) =>
  codeToHtml(code, { lang, theme, decorations });
