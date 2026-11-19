<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useData } from 'vitepress'

const props = defineProps<{ code: string }>()
const { isDark } = useData()
const container = ref<HTMLElement | null>(null)
const source = computed(() => decodeURIComponent(props.code))
let counter = 0

function escapeHtml(value: string) {
  return value
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

async function render() {
  if (!container.value) return
  const mermaid = (await import('mermaid')).default
  mermaid.initialize({
    startOnLoad: false,
    theme: isDark.value ? 'dark' : 'neutral',
  })
  try {
    const { svg } = await mermaid.render(`mermaid-${counter++}`, source.value)
    container.value.innerHTML = svg
  } catch {
    container.value.innerHTML = `<pre class="mermaid-error">${escapeHtml(source.value)}</pre>`
  }
}

onMounted(render)
watch([source, isDark], render)
</script>

<template>
  <div ref="container" class="mermaid-diagram" />
</template>
