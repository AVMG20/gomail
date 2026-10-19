<script lang="ts">
// Lightweight tooltip: tags its child with data-tip attributes; a single global
// <TooltipLayer> renders the bubble. Far cheaper than one tooltip instance per
// button (matters in virtualised rows) and keeps reka's Tooltip out of the bundle.
import { cloneVNode, Comment, defineComponent, type PropType, type VNode } from 'vue'

export default defineComponent({
  name: 'Tip',
  props: {
    label: { type: String, default: '' },
    shortcut: { type: String, default: '' },
    side: { type: String as PropType<'top' | 'bottom' | 'left' | 'right'>, default: 'bottom' },
    disabled: { type: Boolean, default: false },
  },
  setup(props, { slots }) {
    return () => {
      const children: VNode[] = slots.default?.() ?? []
      if (props.disabled || !props.label) return children
      const idx = children.findIndex((v) => v.type !== Comment)
      if (idx < 0) return children
      const tagged = cloneVNode(children[idx], {
        'data-tip': props.label,
        'data-tip-side': props.side,
        ...(props.shortcut ? { 'data-tip-keys': props.shortcut } : {}),
      })
      return children.length === 1 ? tagged : children.map((c, i) => (i === idx ? tagged : c))
    }
  },
})
</script>
