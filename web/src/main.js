import { createApp } from 'vue'
import { createPinia } from 'pinia'
import {
  ElAlert, ElAside, ElButton, ElCard, ElCheckbox, ElCollapse, ElCollapseItem,
  ElContainer, ElDatePicker, ElDialog, ElDrawer, ElDropdown, ElDropdownItem,
  ElDropdownMenu, ElEmpty, ElForm, ElFormItem, ElHeader, ElIcon, ElInput,
	ElInputNumber, ElLoading, ElMain, ElMenu, ElMenuItem, ElOption, ElPopconfirm, ElRadioButton,
  ElRadioGroup, ElSelect, ElSwitch, ElTable, ElTableColumn, ElTag, ElTooltip,
  ElUpload
} from 'element-plus'
import 'element-plus/es/components/alert/style/css'
import 'element-plus/es/components/aside/style/css'
import 'element-plus/es/components/button/style/css'
import 'element-plus/es/components/card/style/css'
import 'element-plus/es/components/checkbox/style/css'
import 'element-plus/es/components/collapse/style/css'
import 'element-plus/es/components/collapse-item/style/css'
import 'element-plus/es/components/config-provider/style/css'
import 'element-plus/es/components/container/style/css'
import 'element-plus/es/components/date-picker/style/css'
import 'element-plus/es/components/dialog/style/css'
import 'element-plus/es/components/drawer/style/css'
import 'element-plus/es/components/dropdown/style/css'
import 'element-plus/es/components/dropdown-item/style/css'
import 'element-plus/es/components/dropdown-menu/style/css'
import 'element-plus/es/components/empty/style/css'
import 'element-plus/es/components/form/style/css'
import 'element-plus/es/components/form-item/style/css'
import 'element-plus/es/components/header/style/css'
import 'element-plus/es/components/icon/style/css'
import 'element-plus/es/components/input/style/css'
import 'element-plus/es/components/input-number/style/css'
import 'element-plus/es/components/loading/style/css'
import 'element-plus/es/components/main/style/css'
import 'element-plus/es/components/menu/style/css'
import 'element-plus/es/components/menu-item/style/css'
import 'element-plus/es/components/message/style/css'
import 'element-plus/es/components/option/style/css'
import 'element-plus/es/components/popconfirm/style/css'
import 'element-plus/es/components/radio-button/style/css'
import 'element-plus/es/components/radio-group/style/css'
import 'element-plus/es/components/select/style/css'
import 'element-plus/es/components/switch/style/css'
import 'element-plus/es/components/table/style/css'
import 'element-plus/es/components/table-column/style/css'
import 'element-plus/es/components/tag/style/css'
import 'element-plus/es/components/tooltip/style/css'
import 'element-plus/es/components/upload/style/css'
import './styles/theme.css'
import App from './App.vue'
import router from './router'
import i18n from './locales'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.use(i18n)
for (const component of [
  ElAlert, ElAside, ElButton, ElCard, ElCheckbox, ElCollapse, ElCollapseItem,
  ElContainer, ElDatePicker, ElDialog, ElDrawer, ElDropdown, ElDropdownItem,
  ElDropdownMenu, ElEmpty, ElForm, ElFormItem, ElHeader, ElIcon, ElInput,
	ElInputNumber, ElMain, ElMenu, ElMenuItem, ElOption, ElPopconfirm, ElRadioButton, ElRadioGroup,
  ElSelect, ElSwitch, ElTable, ElTableColumn, ElTag, ElTooltip, ElUpload
]) app.component(component.name, component)
app.use(ElLoading)
app.mount('#app')
