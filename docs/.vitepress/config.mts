import { defineConfig } from 'vitepress'

export default defineConfig({
  lang: 'pt-BR',
  title: 'Tucano Indices',
  description:
    'Documentação do backend de índices e séries econômicas do mercado brasileiro.',
  srcExclude: ['README.md'],
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: '/logo.svg' }]],
  markdown: {
    config(md) {
      const fence = md.renderer.rules.fence
      if (!fence) return
      md.renderer.rules.fence = (tokens, idx, options, env, self) => {
        const token = tokens[idx]
        if (token.info.trim().split(/\s+/)[0] === 'mermaid') {
          return `<Mermaid code="${encodeURIComponent(token.content)}" />\n`
        }
        return fence(tokens, idx, options, env, self)
      }
    },
  },
  themeConfig: {
    logo: '/logo.svg',
    nav: [
      { text: 'Introdução', link: '/introducao' },
      { text: 'API', link: '/api' },
      { text: 'Operação', link: '/operacao/index-sync' },
      { text: 'Workers', link: '/workers/' },
    ],
    sidebar: [
      {
        text: 'Visão geral',
        items: [{ text: 'Introdução', link: '/introducao' }],
      },
      {
        text: 'Interface',
        items: [{ text: 'API HTTP', link: '/api' }],
      },
      {
        text: 'Operação',
        collapsed: false,
        items: [
          { text: 'Sincronização', link: '/operacao/index-sync' },
          { text: 'Scheduler', link: '/operacao/scheduler' },
          { text: 'Persistência e revisões', link: '/operacao/persistence' },
        ],
      },
      {
        text: 'Workers',
        collapsed: false,
        items: [
          { text: 'Visão geral', link: '/workers/' },
          { text: 'SGS', link: '/workers/sgs' },
          { text: 'Focus', link: '/workers/focus' },
          { text: 'Tesouro', link: '/workers/tesouro' },
          { text: 'B3', link: '/workers/b3' },
          { text: 'Selic', link: '/workers/selic' },
        ],
      },
    ],
    search: {
      provider: 'local',
    },
    outline: {
      level: [2, 3],
      label: 'Nesta página',
    },
    docFooter: {
      prev: 'Anterior',
      next: 'Próxima',
    },
    footer: {
      message: 'Documentação técnica do Tucano Indices.',
    },
  },
})
