import json
import os
import sys
import time
from pathlib import Path

from tencentcloud.common import credential
from tencentcloud.common.exception.tencent_cloud_sdk_exception import TencentCloudSDKException
from tencentcloud.scf.v20180416 import models, scf_client


def release():
    client = scf_client.ScfClient(
        credential.Credential(os.environ['TENCENT_SECRET_ID'], os.environ['TENCENT_SECRET_KEY']),
        os.environ['SCF_REGION'],
    )

    def call(action, **params):
        request = getattr(models, action + 'Request')()
        request.from_json_string(json.dumps({'FunctionName': 'certcenter', 'Namespace': 'default', **params}))
        for attempt in range(10):
            try:
                return json.loads(getattr(client, action)(request).to_json_string())
            except TencentCloudSDKException as error:
                if action not in ('GetFunction', 'GetProvisionedConcurrencyConfig') or error.code != 'InternalError' or attempt == 9:
                    raise
                time.sleep(3)

    aliases = call('ListAliases')['Aliases']
    previous = next((alias['FunctionVersion'] for alias in aliases if alias['Name'] == '$DEFAULT'), None)
    checkpoint = Path('.tmp/scf-previous-version')
    if '--remember' in sys.argv:
        checkpoint.write_text(previous or '$LATEST')
        print('Recorded current default version:', previous, flush=True)
        return
    if checkpoint.exists():
        previous = checkpoint.read_text().strip()
    version = call('PublishVersion', Description=os.environ.get('GITHUB_SHA', 'local deployment'))['FunctionVersion']
    print('Published version:', version, flush=True)
    for attempt in range(60):
        status = call('GetFunction', Qualifier=version)['Status']
        if status == 'Active':
            break
        if status == 'Failed':
            raise RuntimeError('Published function failed to become active')
        time.sleep(5)
    else:
        raise RuntimeError('Published function activation timed out')

    try:
        call('PutProvisionedConcurrencyConfig', Qualifier=version, VersionProvisionedConcurrencyNum=1)
        print('Warming one provisioned instance', flush=True)
        for attempt in range(120):
            allocated = call('GetProvisionedConcurrencyConfig')['Allocated']
            state = next((item for item in allocated if item['Qualifier'] == version), {})
            if state.get('AvailableProvisionedConcurrencyNum', 0) >= 1 and state.get('Status') == 'Done':
                break
            if state.get('Status') == 'Failed':
                raise RuntimeError('Provisioned instance failed; inspect SCF initialization logs')
            time.sleep(5)
        else:
            raise RuntimeError('Provisioned instance initialization timed out')

    except (TencentCloudSDKException, RuntimeError):
        try:
            call('DeleteProvisionedConcurrencyConfig', Qualifier=version)
        except TencentCloudSDKException as cleanup_error:
            print('Provision cleanup requires attention:', version, cleanup_error.code, file=sys.stderr)
        raise

    call('UpdateAlias', Name='$DEFAULT', FunctionVersion=version, RoutingConfig={'AdditionalVersionWeights': []})
    print('Default traffic now uses version:', version, flush=True)
    if previous and previous not in ('$LATEST', version):
        allocated = call('GetProvisionedConcurrencyConfig')['Allocated']
        if any(item['Qualifier'] == previous for item in allocated):
            call('DeleteProvisionedConcurrencyConfig', Qualifier=previous)
            print('Released previous provisioned instance:', previous, flush=True)


if __name__ == '__main__':
    try:
        release()
    except TencentCloudSDKException as error:
        print('SCF release failed:', error.code, file=sys.stderr)
        sys.exit(1)
    except (KeyError, RuntimeError) as error:
        print('SCF release failed:', error, file=sys.stderr)
        sys.exit(1)
