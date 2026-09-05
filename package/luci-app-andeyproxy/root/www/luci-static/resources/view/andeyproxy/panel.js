'use strict';
'require view';
'require uci';

return view.extend({
	handleSave: null,
	handleSaveApply: null,
	handleReset: null,

	render: function() {
		return uci.load('andey-proxy').then(function() {
			var port = uci.get('andey-proxy', 'main', 'port') || '16601';
			var scheme = uci.get('andey-proxy', 'main', 'admin_http') === '1' ? 'http' : 'https';
			var host = window.location.hostname;
			if (host.indexOf(':') !== -1)
				host = '[' + host + ']';
			var url = scheme + '://' + host + ':' + port + '/';

			return E('div', {}, [
				E('div', { 'class': 'cbi-section' }, [
					E('p', {}, [
						E('a', {
							'href': url,
							'target': '_blank',
							'rel': 'noopener noreferrer',
							'class': 'cbi-button cbi-button-action'
						}, _('新窗口打开管理面板')),
						' ',
						E('span', { 'style': 'color:#888' },
							_('管理面板默认使用 HTTPS，首次密码随机生成并仅在启动日志显示一次'))
					]),
					E('p', {}, _('为防止管理会话被嵌入劫持，面板只在 HTTPS 新窗口中打开。')),
					E('p', {}, [
						_('Google Authenticator 可在面板右上角“账户安全”中绑定。若验证器和恢复码均丢失，请先停止服务，再在设备终端执行：'),
						E('code', { 'style': 'display:block;margin-top:.5em;white-space:pre-wrap' },
							'/usr/bin/andey-proxy -cd /etc/andey-proxy -reset-totp')
					])
				])
			]);
		});
	}
});
