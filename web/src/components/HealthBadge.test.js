import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import HealthBadge from './HealthBadge.vue'

describe('HealthBadge', () => {
  it('shows a check mark without issues', () => {
    expect(mount(HealthBadge).text()).toBe('✓')
  })

  it('shows the issue count and warning style', () => {
    const wrapper = mount(HealthBadge, { props: { issues: 3 } })
    expect(wrapper.text()).toBe('3')
    expect(wrapper.classes()).toContain('warning')
  })
})
