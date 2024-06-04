import requests
import json

def invoke_function():
    url = "http://10.62.0.1:8081/function/nodeinfo"
    headers = {
        'Content-Type': 'application/json',
        'UserID': 'user-1'
    }
    data = json.dumps({"data": "value"})
    
    response = requests.post(url, headers=headers, data=data)
    
    if response.status_code == 200:
        print("Success:", response.text)
    else:
        print("Error:", response.text)

if __name__ == "__main__":
    invoke_function()
