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
			var url = window.location.protocol + '//' + window.location.hostname + ':' + port + '/';

			return E('div', {}, [
				E('div', { 'class': 'cbi-section' }, [
					E('p', {}, [
						E('a', {
							'href': url,
							'target': '_blank',
							'class': 'cbi-button cbi-button-action'
						}, _('新窗口打开管理面板')),
						' ',
						E('span', { 'style': 'color:#888' },
							_('默认账号密码 666 / 666，首次登录请修改'))
					]),
					E('iframe', {
						'src': url,
						'style': 'width:100%;height:78vh;border:1px solid #ccc;border-radius:4px;background:#fff'
					})
				])
			]);
		});
	}
});
