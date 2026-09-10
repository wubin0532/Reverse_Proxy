import { createApp } from 'vue'
import { createPinia } from 'pinia'
import element from './plugins/element'
import './styles/theme.css'
import App from './App.vue'
import router from './router'
import i18n from './locales'

const app = createApp(App)
app.use(createPinia())
app.use(router)
app.use(i18n)
app.use(element)
app.mount('#app')
