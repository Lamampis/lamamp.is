# lamamp.is
Repository for my personal website.

Check it out at www.lamamp.is

Build with ```go build -o lamamp```

Run with ```./lamamp --prod```

Or just ```go run main.go --prod```

To test locally remove the ```--prod``` flag

To run with [Air:](https://github.com/air-verse/air) (auto reload):

```air```

```air -- --prod```

Update with ```git pull origin main```

Autoupdate with crontab:
```crontab -e```
```* * * * * cd lamamp.is && git pull origin main > /dev/null 2>&1```
