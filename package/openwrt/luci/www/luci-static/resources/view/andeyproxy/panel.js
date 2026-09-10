'use strict';
'require view';
'require uci';

return view.extend({
	handleSave: null,
	handleSaveApply: null,
	handleReset: null,

	render: function() {
		return uci.load('andey-proxy').then(function() {
			var port = uci.get('andey-proxy', 'main', 'port') || '16606';
			var scheme = uci.get('andey-proxy', 'main', 'admin_http') === '1' ? 'http' : 'https';
			var host = window.location.hostname;
			if (host.indexOf(':') !== -1)
				host = '[' + host + ']';
			var url = scheme + '://' + host + ':' + port + '/';

			return E('div', {}, [
				E('div', { 'class': 'cbi-section' }, [
					E('p', {}, [
						_('Admin panel URL:'),
						' ',
						E('a', {
							'href': url,
							'target': '_blank',
							'rel': 'noopener noreferrer'
						}, url)
					]),
					E('p', {}, [
						E('a', {
							'href': url,
							'target': '_blank',
							'rel': 'noopener noreferrer',
							'class': 'cbi-button cbi-button-action'
						}, _('Open admin panel in new window')),
						' ',
						E('span', { 'style': 'color:#888' },
							_('The admin panel uses HTTPS by default. The initial password is randomly generated and shown only once in the startup log.'))
					]),
					E('p', {}, _('To prevent the admin session from being hijacked by embedding, the panel only opens in a new HTTPS window.')),
					E('p', {}, [
						_('Google Authenticator can be bound under "Account Security" at the top right of the panel. If both the authenticator and the recovery codes are lost, stop the service first, then run on the device terminal:'),
						E('code', { 'style': 'display:block;margin-top:.5em;white-space:pre-wrap' },
							'/usr/bin/andey-proxy -cd /etc/andey-proxy -reset-totp')
					])
				])
			]);
		});
	}
});
