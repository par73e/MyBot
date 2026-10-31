from test_platform import clients
from app.main import STATIC


def test_only_fingerprinted_public_images_are_immutable(clients):
    client,_=clients
    images=list(STATIC.glob('home-background-*.webp'))
    assert len(images)==2
    for image in images:
        url='/asrobot/static/'+image.name
        response=client.get(url)
        assert response.status_code==200
        assert response.headers['cache-control']=='public, max-age=31536000, immutable'
        assert response.headers['content-type']=='image/webp'
        cached=client.get(url,headers={'If-None-Match':response.headers['etag']})
        assert cached.status_code==304 and not cached.content
        assert cached.headers['cache-control']==response.headers['cache-control']
    css=client.get('/asrobot/static/style.css')
    assert css.headers['cache-control']=='public, max-age=0, must-revalidate'
    assert client.get('/asrobot/static/missing.webp').headers['cache-control']=='no-store'


def test_personal_pages_and_api_still_never_cache(clients):
    client,_=clients
    for path in ('/asrobot/','/asrobot/api/me','/asrobot/api/settings','/asrobot/api/messages','/asrobot/api/export'):
        response=client.get(path)
        assert response.headers['cache-control']=='no-store'
